package storage

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

type SnapshotLayer struct {
	ID        int64
	FileID    int64
	Active    bool // whether of not it is the current active layer that is being written to
	VersionID int64
	Tag       string
	chunks    map[uint64][]byte
}

func newSnapshotLayer(fileID int64) *SnapshotLayer {
	return &SnapshotLayer{
		FileID:    fileID,
		Active:    false,
		VersionID: 0,
		chunks:    make(map[uint64][]byte),
	}
}

// addChunk adds data at the specified offset within the layer
func (l *SnapshotLayer) addChunk(off uint64, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	l.chunks[off] = cp
}

type Manager struct {
	mu  sync.RWMutex // Primary mutex for protecting all shared state
	db  *sql.DB
	log *log.Logger
}

// NewManager creates (or reloads) a StorageManager using the provided metadataStore.
func NewManager(db *sql.DB, log *log.Logger) *Manager {
	managerLog := log.With()
	managerLog.SetPrefix("💽 storage")

	sm := &Manager{
		db:  db,
		log: managerLog,
	}

	return sm
}

// WriteFile writes data to the active layer at the specified offset.
// It returns the active layer's ID and the offset where the data was written.
func (sm *Manager) WriteFile(filename string, data []byte, offset uint64) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.log.Debug("Writing data", "filename", filename, "size", len(data), "offset", offset)

	// Get the file ID from the file name
	fileID, err := sm.GetFileIDByName(filename)
	if err != nil {
		sm.log.Error("Failed to get file ID", "filename", filename, "error", err)
		return fmt.Errorf("failed to get file ID: %w", err)
	}
	if fileID == 0 {
		sm.log.Error("File not found", "filename", filename)
		return fmt.Errorf("file not found")
	}

	// Get the active layer
	query := `SELECT id FROM snapshot_layers WHERE file_id = $1 AND active = 1 ORDER BY id ASC LIMIT 1;`
	var layerID int64
	err = sm.db.QueryRow(query, fileID).Scan(&layerID)
	if err != nil {
		sm.log.Error("Failed to query active layer", "filename", filename, "error", err)
		return fmt.Errorf("failed to query active layer: %w", err)
	}

	fileSize, err := sm.calcFileSize(fileID)
	if err != nil {
		sm.log.Error("Failed to calculate file size", "fileID", fileID, "error", err)
		return fmt.Errorf("failed to calculate file size: %w", err)
	}

	if offset > fileSize {
		sm.log.Error("Write offset is beyond file size", "offset", offset, "fileSize", fileSize)
		return fmt.Errorf("write offset is beyond file size")
	}

	sm.log.Debug("Added chunk to active layer", "layerID", layerID, "offset", offset, "size", len(data))

	// Calculate ranges and record chunk in the database
	layerRange, fileRange, err := sm.calculateRanges(layerID, offset, len(data))

	if err != nil {
		sm.log.Error("Failed to calculate ranges", "layerID", layerID, "offset", offset, "error", err)
		return fmt.Errorf("failed to calculate ranges: %w", err)
	}

	if err = sm.insertChunk(layerID, offset, data, layerRange, fileRange); err != nil {
		sm.log.Error("Failed to record chunk", "layerID", layerID, "offset", offset, "error", err)
		return fmt.Errorf("failed to record chunk: %w", err)
	}

	sm.log.Debug("Data written successfully", "layerID", layerID, "offset", offset, "size", len(data))
	return nil
}

// calculateRanges computes the layer-relative and file-absolute ranges for a chunk
func (sm *Manager) calculateRanges(layerID int64, offset uint64, dataSize int) ([2]uint64, [2]uint64, error) {
	dataLength := uint64(dataSize)

	// Retrieve the layer base from the metadata store.
	baseOffset, err := sm.GetLayerBase(layerID)
	if err != nil {
		sm.log.Error("Failed to retrieve layer base", "layerID", layerID, "error", err)
		return [2]uint64{}, [2]uint64{}, fmt.Errorf("failed to retrieve layer base: %w", err)
	}

	var layerStart uint64
	if offset >= baseOffset {
		layerStart = offset - baseOffset
	} else {
		layerStart = 0
	}
	layerEnd := layerStart + dataLength
	layerRange := [2]uint64{layerStart, layerEnd}

	// File range remains the global offset range.
	fileStart := offset
	fileEnd := offset + dataLength
	fileRange := [2]uint64{fileStart, fileEnd}

	sm.log.Debug("Calculated ranges for chunk",
		"layerID", layerID,
		"layerRange", layerRange,
		"fileRange", fileRange)

	return layerRange, fileRange, nil
}

func (sm *Manager) FileSize(id int64) (uint64, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.calcFileSize(id)
}

// ReadFileOption defines functional options for GetDataRange
type ReadFileOption func(*readFileOptions)

// readFileOptions holds all options for GetDataRange
type readFileOptions struct {
	version string
}

// WithVersionTag specifies a version tag to retrieve data up to
func WithVersionTag(version string) ReadFileOption {
	return func(opts *readFileOptions) {
		opts.version = version
	}
}

// ReadFile returns a slice of data from the given offset up to size bytes.
// Optional version tag can be specified to retrieve data up to a specific version.
func (sm *Manager) ReadFile(filename string, offset uint64, size uint64, opts ...ReadFileOption) ([]byte, error) {
	// Process options
	options := readFileOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	hasVersion := options.version != ""

	if hasVersion {
		sm.log.Debug("reading file",
			"filename", filename,
			"offset", offset,
			"size", size,
			"version", options.version)
	} else {
		sm.log.Debug("reading file",
			"filename", filename,
			"offset", offset,
			"size", size)
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Get the file ID from the file name
	id, err := sm.GetFileIDByName(filename)
	if id == 0 {
		sm.log.Error("File not found", "filename", filename)
		return nil, fmt.Errorf("file not found")
	}
	if err != nil {
		sm.log.Error("Failed to get file ID", "filename", filename, "error", err)
		return nil, fmt.Errorf("failed to get file ID: %w", err)
	}

	var layers []*SnapshotLayer

	// check if there's a layer for this file with the given version tag
	if hasVersion {
		exists := false
		err = sm.db.QueryRow("SELECT EXISTS(SELECT 1 FROM snapshot_layers inner join versions on versions.id = snapshot_layers.version_id WHERE snapshot_layers.file_id = $1 and versions.tag = $2)", id, options.version).Scan(&exists)
		if err != nil {
			sm.log.Error("failed to check if layer exists", "id", id, "version", options.version, "error", err)
			return nil, fmt.Errorf("failed to check if layer exists: %w", err)
		}
		if !exists {
			sm.log.Error("version tag not found", "version", options.version, "filename", filename)
			return []byte{}, fmt.Errorf("version tag not found")
		}
	}

	// Load layers for this specific file
	allLayers, err := sm.LoadLayersByFileID(id)
	if err != nil {
		sm.log.Error("failed to load layers", "id", id, "error", err)
		if hasVersion {
			return nil, fmt.Errorf("failed to load layers: %w", err)
		}
		return []byte{}, nil
	}

	// If version tag is specified, filter layers by version
	if hasVersion {
		// Filter layers up to the specified version
		for _, layer := range allLayers {
			layers = append(layers, layer)
			if layer.Tag == options.version {
				break
			}
		}
	} else {
		// Use all layers when no version is specified
		layers = allLayers
	}

	// Load all chunks
	chunksMap, err := sm.loadChunksBySnapshotLayerID()
	if err != nil {
		sm.log.Error("Failed to load chunks from metadata store", "error", err)
		if hasVersion {
			return nil, fmt.Errorf("failed to load chunks: %w", err)
		}
		return []byte{}, nil
	}

	// Calculate maximum size by finding the highest offset + data length
	var maxSize uint64 = 0

	for _, layer := range layers {
		if chunks, ok := chunksMap[int64(layer.ID)]; ok {
			for _, chunk := range chunks {
				endOffset := chunk.Offset + uint64(len(chunk.Data))
				if endOffset > maxSize {
					maxSize = endOffset
				}
			}
		}
	}

	// Create buffer of appropriate size
	buf := make([]byte, maxSize)

	// Merge layers in order (later layers override earlier ones)
	for _, layer := range layers {
		if chunks, ok := chunksMap[int64(layer.ID)]; ok {
			// Chunks are already sorted by offset from the database
			for _, chunk := range chunks {
				if chunk.Offset+uint64(len(chunk.Data)) <= uint64(len(buf)) {
					copy(buf[chunk.Offset:chunk.Offset+uint64(len(chunk.Data))], chunk.Data)
				} else {
					// Handle case where chunk extends beyond current buffer
					newSize := chunk.Offset + uint64(len(chunk.Data))
					newBuf := make([]byte, newSize)
					copy(newBuf, buf)
					copy(newBuf[chunk.Offset:], chunk.Data)
					buf = newBuf
				}
			}
		}
	}

	// Apply offset and size limits to the full content
	if offset >= uint64(len(buf)) {
		sm.log.Debug("Requested offset beyond content size", "offset", offset, "contentSize", len(buf))
		return []byte{}, nil
	}

	end := min(offset+size, uint64(len(buf)))

	// Create a copy of the slice to prevent race conditions
	result := make([]byte, end-offset)
	copy(result, buf[offset:end])

	if hasVersion {
		sm.log.Debug("Returning data range with version",
			"offset", offset,
			"end", end,
			"returnedSize", end-offset,
			"version", options.version)
	} else {
		sm.log.Debug("Returning data range",
			"offset", offset,
			"end", end,
			"returnedSize", end-offset)
	}

	return result, nil
}

// GetLayerBase returns the chunk with lowest start file offset for a given layer.
func (sm *Manager) GetLayerBase(layerID int64) (uint64, error) {
	query := `
		SELECT lower(file_range)::bigint
		FROM chunks
		WHERE snapshot_layer_id = $1
		ORDER BY lower(file_range) ASC
		LIMIT 1;
	`

	var base sql.NullInt64
	err := sm.db.QueryRow(query, layerID).Scan(&base)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil // base will be 0
		}
		return 0, err
	}
	if !base.Valid {
		return 0, fmt.Errorf("invalid base value for layer %d", layerID)
	}
	return uint64(base.Int64), nil
}

// LayerMetadata holds metadata for a layer.
type LayerMetadata struct {
	ID        int
	Base      int64
	CreatedAt time.Time
	Active    bool
}

// Chunk holds data for a write chunk.
type Chunk struct {
	LayerID    int64
	Offset     uint64
	Data       []byte
	LayerRange [2]uint64 // Range within a layer as an array of two integers
	FileRange  [2]uint64 // Range within the virtual file as an array of two integers
}

// insertActiveLayer inserts a new layer record, making it the active layer for the file.
// It accepts optional functional parameters for configuration.
func (sm *Manager) insertActiveLayer(layer *SnapshotLayer, opts ...queryOption) (int64, error) {
	sm.log.Debug("Recording new layer in metadata store", "layerID", layer.ID)

	// Process options
	options := queryOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	query := `INSERT INTO snapshot_layers (file_id, active) VALUES ($1, 1) RETURNING id;`
	var newID int64
	var err error

	if options.tx != nil {
		// Use the provided transaction
		err = options.tx.QueryRow(query, layer.FileID).Scan(&newID)
	} else {
		// Use the database connection directly
		err = sm.db.QueryRow(query, layer.FileID).Scan(&newID)
	}

	if err != nil {
		sm.log.Error("Failed to record new layer", "layerID", layer.ID, "error", err)
		return 0, err
	}

	layer.ID = newID

	sm.log.Debug("Layer recorded successfully", "layerID", newID)
	return newID, nil
}

// InsertFile inserts a new file into the files table and returns its ID.
func (sm *Manager) InsertFile(name string) (int64, error) {
	sm.log.Debug("Inserting new file into metadata store", "name", name)

	query := `INSERT INTO files (name) VALUES ($1) RETURNING id;`
	var fileID int64
	err := sm.db.QueryRow(query, name).Scan(&fileID)
	if err != nil {
		sm.log.Error("Failed to insert new file", "name", name, "error", err)
		return 0, err
	}

	// After inserting the file, create an initial active layer for it
	layer := newSnapshotLayer(fileID)
	_, err = sm.insertActiveLayer(layer)
	if err != nil {
		sm.log.Error("Failed to create initial layer for new file", "fileID", fileID, "error", err)
		return 0, fmt.Errorf("failed to create initial layer for new file: %w", err)
	}

	sm.log.Debug("File inserted successfully", "name", name, "fileID", fileID)
	return fileID, nil
}

type queryOption func(*queryOptions)

type queryOptions struct {
	tx *sql.Tx
}

func withTx(tx *sql.Tx) queryOption {
	return func(opts *queryOptions) {
		opts.tx = tx
	}
}

func (sm *Manager) GetFileIDByName(name string, opts ...queryOption) (int64, error) {
	query := `SELECT id FROM files WHERE name = $1;`
	var fileID int64

	options := queryOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	var err error

	if options.tx != nil {
		err = options.tx.QueryRow(query, name).Scan(&fileID)
	} else {
		err = sm.db.QueryRow(query, name).Scan(&fileID)
	}

	if err != nil {
		if err == sql.ErrNoRows {
			sm.log.Warn("File not found", "name", name)
			return 0, nil
		}
		sm.log.Error("Failed to retrieve file ID", "name", name, "error", err)
		return 0, err
	}

	return fileID, nil
}

// insertChunk inserts a new chunk record.
// Now includes layer_range and file_range parameters
func (sm *Manager) insertChunk(layerID int64, offset uint64, data []byte, layerRange [2]uint64, fileRange [2]uint64) error {
	sm.log.Debug("Inserting chunk in the database",
		"layerID", layerID,
		"offset", offset,
		"dataSize", len(data),
		"layerRange", layerRange,
		"fileRange", fileRange)

	layerRangeStr := fmt.Sprintf("[%d,%d)", layerRange[0], layerRange[1])
	fileRangeStr := fmt.Sprintf("[%d,%d)", fileRange[0], fileRange[1])

	query := `INSERT INTO chunks (snapshot_layer_id, offset_value, data, layer_range, file_range) 
	         VALUES ($1, $2, $3, $4, $5);`
	_, err := sm.db.Exec(query, layerID, offset, data, layerRangeStr, fileRangeStr)
	if err != nil {
		sm.log.Error("Failed to insert chunk", "layerID", layerID, "offset", offset, "error", err)
		return err
	}

	sm.log.Debug("Chunk inserted successfully", "layerID", layerID, "offset", offset, "dataSize", len(data))
	return nil
}

// loadChunksBySnapshotLayerID loads all chunk records from the database and groups them by snapshot_layer_id.
func (sm *Manager) loadChunksBySnapshotLayerID() (map[int64][]Chunk, error) {
	sm.log.Debug("Loading chunks from metadata store")

	query := `SELECT snapshot_layer_id, offset_value, data, layer_range, file_range FROM chunks ORDER BY snapshot_layer_id, offset_value ASC;`
	rows, err := sm.db.Query(query)
	if err != nil {
		sm.log.Error("Failed to query chunks", "error", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]Chunk)
	chunksCount := 0
	totalDataSize := 0

	for rows.Next() {
		var layerID int64
		var offset uint64
		var data []byte
		var layerRangeStr, fileRangeStr sql.NullString

		if err := rows.Scan(&layerID, &offset, &data, &layerRangeStr, &fileRangeStr); err != nil {
			sm.log.Error("Error scanning chunk row", "error", err)
			return nil, err
		}

		layerRange := [2]uint64{0, 0}
		if layerRangeStr.Valid {
			parts := strings.Split(strings.Trim(layerRangeStr.String, "[)"), ",")
			if len(parts) == 2 {
				start, err := strconv.ParseUint(parts[0], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing layer range start", "value", parts[0], "error", err)
					return nil, err
				}
				end, err := strconv.ParseUint(parts[1], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing layer range end", "value", parts[1], "error", err)
					return nil, err
				}
				layerRange[0] = start
				layerRange[1] = end
			}
		}

		fileRange := [2]uint64{0, 0}
		if fileRangeStr.Valid {
			parts := strings.Split(strings.Trim(fileRangeStr.String, "[)"), ",")
			if len(parts) == 2 {
				start, err := strconv.ParseUint(parts[0], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing file range start", "value", parts[0], "error", err)
					return nil, err
				}
				end, err := strconv.ParseUint(parts[1], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing file range end", "value", parts[1], "error", err)
					return nil, err
				}
				fileRange[0] = start
				fileRange[1] = end
			}
		}

		result[layerID] = append(result[layerID], Chunk{
			LayerID:    layerID,
			Offset:     offset,
			Data:       data,
			LayerRange: layerRange,
			FileRange:  fileRange,
		})

		chunksCount++
		totalDataSize += len(data)
		sm.log.Debug("Loaded chunk", "layerID", layerID, "offset", offset, "dataSize", len(data))
	}

	layerCount := len(result)
	sm.log.Debug("Chunks loaded successfully",
		"totalChunks", chunksCount,
		"layerCount", layerCount,
		"totalDataSize", totalDataSize)

	return result, nil
}

// close closes the database.
func (sm *Manager) Close() error {
	sm.log.Debug("Closing metadata store database connection")
	err := sm.db.Close()
	if err != nil {
		sm.log.Error("Error closing database connection", "error", err)
	} else {
		sm.log.Debug("Database connection closed successfully")
	}
	return err
}

// calcFileSize calculates the total byte size of the virtual file from all layers and their chunks, respecting layer creation order and handling overlapping file ranges.
func (sm *Manager) calcFileSize(fileID int64) (uint64, error) {
	query := `
		SELECT e.file_range
		FROM chunks e
		JOIN snapshot_layers l ON e.snapshot_layer_id = l.id
		WHERE l.file_id = $1
		ORDER BY l.created_at ASC, lower(e.file_range) ASC;
	`
	rows, err := sm.db.Query(query, fileID)
	if err != nil {
		sm.log.Error("Failed to query file ranges", "error", err, "fileID", fileID)
		return 0, err
	}
	defer rows.Close()

	type Range struct {
		start uint64
		end   uint64
	}

	var ranges []Range

	for rows.Next() {
		var fileRangeStr sql.NullString

		if err := rows.Scan(&fileRangeStr); err != nil {
			sm.log.Error("Error scanning file range row", "error", err)
			return 0, err
		}

		if fileRangeStr.Valid {
			parts := strings.Split(strings.Trim(fileRangeStr.String, "[)"), ",")
			if len(parts) == 2 {
				start, err := strconv.ParseUint(parts[0], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing file range start", "value", parts[0], "error", err)
					return 0, err
				}
				end, err := strconv.ParseUint(parts[1], 10, 64)
				if err != nil {
					sm.log.Error("Error parsing file range end", "value", parts[1], "error", err)
					return 0, err
				}
				ranges = append(ranges, Range{start: start, end: end})
			}
		}
	}

	if err = rows.Err(); err != nil {
		sm.log.Error("Error iterating over file range rows", "error", err)
		return 0, err
	}

	// Merge overlapping ranges
	var totalSize uint64
	if len(ranges) > 0 {
		// Ranges are already sorted by the SQL query (ORDER BY l.created_at ASC, lower(e.file_range) ASC)
		// Find the maximum end position, which represents the virtual file size
		var maxEnd uint64
		for _, r := range ranges {
			if r.end > maxEnd {
				maxEnd = r.end
			}
		}

		totalSize = maxEnd
	}

	return totalSize, nil
}

// LoadLayersByFileID loads all layers associated with a specific file ID from the database.
func (sm *Manager) LoadLayersByFileID(fileID int64) ([]*SnapshotLayer, error) {
	sm.log.Debug("Loading layers for file from metadata store", "fileID", fileID)

	query := `
		SELECT snapshot_layers.id, file_id, active, version_id, tag
		FROM snapshot_layers
		LEFT JOIN versions ON snapshot_layers.version_id = versions.id
		WHERE file_id = $1 ORDER BY id ASC;
	`
	rows, err := sm.db.Query(query, fileID)
	if err != nil {
		sm.log.Error("Failed to query layers for file", "fileID", fileID, "error", err)
		return nil, err
	}
	defer rows.Close()

	var layers []*SnapshotLayer
	layerCount := 0

	for rows.Next() {
		var id, activeInt int64
		var versionID sql.NullInt64
		var tag sql.NullString
		if err := rows.Scan(&id, &fileID, &activeInt, &versionID, &tag); err != nil {
			sm.log.Error("Error scanning layer row", "error", err)
			return nil, err
		}

		layer := newSnapshotLayer(fileID)
		layer.ID = id
		if versionID.Valid {
			layer.VersionID = versionID.Int64
		} else {
			layer.VersionID = 0
		}
		if tag.Valid {
			layer.Tag = tag.String
		}
		layers = append(layers, layer)

		active := activeInt != 0
		sm.log.Debug("Loaded layer for file", "layerID", id, "active", active, "versionID", layer.VersionID, "tag", layer.Tag)
		layerCount++
	}

	sm.log.Debug("Layers for file loaded successfully", "fileID", fileID, "count", layerCount)
	return layers, nil
}

// deleteFile removes a file and its associated layers and chunks from the database within a transaction.
// ! FIX(vinimdocarmo): this shouldn't touch the existing layers, but create a new one marking file as deleted.
func (sm *Manager) DeleteFile(name string) error {
	sm.log.Debug("Deleting file and its associated data from metadata store", "name", name)

	// Begin a transaction
	tx, err := sm.db.Begin()
	if err != nil {
		sm.log.Error("Failed to begin transaction", "error", err)
		return err
	}

	// Ensure the transaction is rolled back in case of an error
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p) // re-throw panic after Rollback
		} else if err != nil {
			sm.log.Error("Transaction failed, rolling back", "error", err)
			tx.Rollback()
		} else {
			err = tx.Commit()
			if err != nil {
				sm.log.Error("Failed to commit transaction", "error", err)
			}
		}
	}()

	// Retrieve the file ID
	fileID, err := sm.GetFileIDByName(name, withTx(tx))
	if err != nil {
		sm.log.Error("Failed to retrieve file ID", "name", name, "error", err)
		return err
	}

	if fileID == 0 {
		sm.log.Error("File not found, nothing to delete", "name", name)
		return nil
	}

	// Delete all chunks associated with the file's layers
	deleteChunksQuery := `DELETE FROM chunks WHERE snapshot_layer_id IN (SELECT id FROM snapshot_layers WHERE file_id = $1);`
	_, err = tx.Exec(deleteChunksQuery, fileID)
	if err != nil {
		sm.log.Error("Failed to delete chunks for file", "name", name, "error", err)
		return err
	}

	// Delete all layers associated with the file
	deleteLayersQuery := `DELETE FROM snapshot_layers WHERE file_id = $1;`
	_, err = tx.Exec(deleteLayersQuery, fileID)
	if err != nil {
		sm.log.Error("Failed to delete layers for file", "name", name, "error", err)
		return err
	}

	// Delete the file itself
	deleteFileQuery := `DELETE FROM files WHERE id = $1;`
	_, err = tx.Exec(deleteFileQuery, fileID)
	if err != nil {
		sm.log.Error("Failed to delete file", "name", name, "error", err)
		return err
	}

	sm.log.Info("File and its associated data deleted successfully", "name", name)
	return nil
}

func (sm *Manager) Checkpoint(filename string, version string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Start transaction
	tx, err := sm.db.Begin()
	if err != nil {
		sm.log.Error("Failed to begin transaction", "error", err)
		return err
	}

	// Setup deferred rollback in case of error or panic
	defer func() {
		if p := recover(); p != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				sm.log.Error("Failed to rollback transaction after panic", "error", rbErr)
			}
			// Re-panic after rollback
			panic(p)
		} else if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				sm.log.Error("Failed to rollback transaction", "error", rbErr)
			}
		}
	}()

	// Get the file ID from the file name
	fileID, err := sm.GetFileIDByName(filename, withTx(tx))
	if err != nil {
		sm.log.Error("Failed to get file ID", "filename", filename, "error", err)
		return fmt.Errorf("failed to get file ID: %w", err)
	}

	// Get the current active snapshot layer ID
	query := `SELECT id FROM snapshot_layers WHERE file_id = $1 AND active = 1 ORDER BY id ASC LIMIT 1;`
	var layerID int64
	err = tx.QueryRow(query, fileID).Scan(&layerID)
	if err != nil {
		sm.log.Error("Failed to query active layer", "filename", filename, "error", err)
		return fmt.Errorf("failed to query active layer: %w", err)
	}

	// Insert version within transaction
	insertVersionQ := `INSERT INTO versions (tag) VALUES ($1) RETURNING id;`
	var versionID int64
	err = tx.QueryRow(insertVersionQ, version).Scan(&versionID)
	if err != nil {
		sm.log.Error("Failed to insert new version", "tag", version, "error", err)
		return err
	}

	// Update layer within transaction
	updateLayerQ := `UPDATE snapshot_layers SET active = 0, version_id = $1 WHERE id = $2;`
	_, err = tx.Exec(updateLayerQ, versionID, layerID)
	if err != nil {
		sm.log.Error("Failed to commit layer", "id", layerID, "error", err)
		return err
	}

	// Create a new active layer within the same transaction
	layer := newSnapshotLayer(fileID)
	layerId, err := sm.insertActiveLayer(layer, withTx(tx))
	if err != nil {
		sm.log.Error("Failed to record new layer", "error", err)
		return fmt.Errorf("failed to record new layer: %w", err)
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		sm.log.Error("Failed to commit transaction", "error", err)
		return err
	}

	sm.log.Debug("Checkpoint successful", "layerID", layerId)

	return nil
}

// fileInfo represents basic file information
type fileInfo struct {
	ID   int
	Name string
}

// GetAllFiles returns a list of all files in the database
func (sm *Manager) GetAllFiles() ([]fileInfo, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	query := `SELECT id, name FROM files;`
	rows, err := sm.db.Query(query)
	if err != nil {
		sm.log.Error("Failed to query files", "error", err)
		return nil, err
	}
	defer rows.Close()

	var files []fileInfo
	for rows.Next() {
		var file fileInfo
		if err := rows.Scan(&file.ID, &file.Name); err != nil {
			sm.log.Error("Failed to scan file row", "error", err)
			return nil, err
		}
		files = append(files, file)
	}

	if err := rows.Err(); err != nil {
		sm.log.Error("Error iterating file rows", "error", err)
		return nil, err
	}

	return files, nil
}

// Truncate changes the size of a file to the specified size.
// If the new size is smaller than the current size, the file is truncated.
// If the new size is larger than the current size, the file is extended with zero bytes.
// ! FIX(vinimdocarmo): this functions should be aware of database transactions.
func (sm *Manager) Truncate(filename string, newSize uint64) error {
	sm.log.Debug("Truncating file", "filename", filename, "newSize", newSize)

	// First, get the file ID and current size without holding any lock
	fileID, err := sm.GetFileIDByName(filename)
	if fileID == 0 {
		sm.log.Error("File not found", "filename", filename)
		return fmt.Errorf("file not found")
	}
	if err != nil {
		sm.log.Error("Failed to get file ID", "filename", filename, "error", err)
		return fmt.Errorf("failed to get file ID: %w", err)
	}

	// Get current file size
	size, err := sm.FileSize(fileID)
	if err != nil {
		sm.log.Error("Failed to calculate file size", "filename", filename, "error", err)
		return fmt.Errorf("failed to calculate file size: %w", err)
	}

	if newSize == size {
		sm.log.Debug("New size equals current size, no truncation needed", "size", newSize)
		return nil
	} else if newSize < size {
		sm.log.Info("New size is smaller than current size. This is not supported.", "filename", filename, "oldSize", size, "newSize", newSize)
		return fmt.Errorf("new size is smaller than current size")
	} else {
		// Calculate how many zero bytes to add
		bytesToAdd := newSize - size

		// Create a buffer of zero bytes
		zeroes := make([]byte, bytesToAdd)

		// Write the zero bytes at the end of the file
		err = sm.WriteFile(filename, zeroes, size)
		if err != nil {
			sm.log.Error("Failed to extend file", "filename", filename, "error", err)
			return fmt.Errorf("failed to extend file: %w", err)
		}

		sm.log.Debug("File extended successfully", "filename", filename, "oldSize", size, "newSize", newSize)
		return nil
	}
}
