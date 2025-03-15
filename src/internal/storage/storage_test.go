package storage_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vinimdocarmo/difffs/src/internal/difffstest"
	"github.com/vinimdocarmo/difffs/src/internal/storage"
)

func TestWriteReadActiveLayer(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_write_read" // Unique file name for testing

	// Insert the file, which should create an initial active layer
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Load layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")
	require.Equal(t, 1, len(layers), "Expected one initial layer")

	input := []byte("hello world")
	err = sm.WriteFile(filename, input, 0)
	require.NoError(t, err, "Write error")

	// Get the file size to read the entire content
	fileSize, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent, err := sm.ReadFile(filename, 0, fileSize)
	require.NoError(t, err, "Failed to get full content")
	assert.Equal(t, len(input), len(fullContent), "Full content length should match input length")

	data, err := sm.ReadFile(filename, 0, uint64(len(input)))
	require.NoError(t, err, "GetDataRange error")
	assert.Equal(t, input, data, "Retrieved data should match input")
}

func TestCheckpointingNewActiveLayer(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_checkpoint_layer" // Unique file name for testing

	// Insert the file, which should create an initial active layer
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Load layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")
	require.Equal(t, 1, len(layers), "Expected one initial layer")

	input1 := []byte("data1")
	err = sm.WriteFile(filename, input1, 0)
	require.NoError(t, err, "Write error")

	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Checkpoint failed")

	db := difffstest.SetupDB(t)
	defer db.Close()

	input2 := []byte("data2")
	err = sm.WriteFile(filename, input2, uint64(len(input1)))
	require.NoError(t, err, "Write error")

	data, err := sm.ReadFile(filename, uint64(len(input1)), uint64(len(input2)))
	require.NoError(t, err, "GetDataRange error")
	assert.Equal(t, input2, data, "Retrieved data should match second input")
}

func TestReadFromActiveLayer(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_read_active" // Unique file name for testing

	// Insert the file, which should create an initial active layer
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write initial data
	input1 := []byte("hello")
	err = sm.WriteFile(filename, input1, 0)
	require.NoError(t, err, "Write error")

	// Seal the layer
	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Failed to commit layer")

	// Write more data
	input2 := []byte(" world")
	err = sm.WriteFile(filename, input2, uint64(len(input1)))
	require.NoError(t, err, "Write error")

	// Read from first layer
	data1, err := sm.ReadFile(filename, 0, uint64(len(input1)))
	require.NoError(t, err, "GetDataRange error")
	assert.Equal(t, input1, data1, "Retrieved data should match first input")

	// Read from second layer
	data2, err := sm.ReadFile(filename, uint64(len(input1)), uint64(len(input2)))
	require.NoError(t, err, "GetDataRange error")
	assert.Equal(t, input2, data2, "Retrieved data should match second input")

	// Read across both layers
	combined := append(input1, input2...)
	data3, err := sm.ReadFile(filename, 0, uint64(len(combined)))
	require.NoError(t, err, "GetDataRange error")
	assert.Equal(t, combined, data3, "Retrieved data should match combined input")
}

func TestPartialRead(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_partial_read" // Unique file name for testing

	// Insert the file, which should create an initial active layer
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Load layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")
	require.Equal(t, 1, len(layers), "Expected one initial layer")

	input := []byte("partial read test")
	err = sm.WriteFile(filename, input, 0)
	require.NoError(t, err, "Write error")

	partialSize := uint64(7)
	data, err := sm.ReadFile(filename, 0, partialSize)
	require.NoError(t, err, "GetDataRange error")

	assert.Equal(t, int(partialSize), len(data), "Partial read should return requested length")
	assert.Equal(t, input[:partialSize], data, "Partial read should return correct data slice")
}

func TestInitialLayerCreationOnFileInsert(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "newfile"

	// Insert the file, which should create an initial active layer
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Load layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")

	// Verify that one layer exists and it is active
	require.Equal(t, 1, len(layers), "Expected one initial layer")
	assert.False(t, layers[0].Active, "Initial layer should be active")
}

func TestGetDataRangeByFileName(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_get_data_range"

	// Insert the file, which should create an initial active layer
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write data to the layer
	data := []byte("test data for GetDataRange by filename")
	err = sm.WriteFile(filename, data, 0)
	require.NoError(t, err, "Failed to write data")

	// Read the data using GetDataRange with filename
	readData, err := sm.ReadFile(filename, 0, uint64(len(data)))
	require.NoError(t, err, "Failed to read data by filename")
	assert.Equal(t, data, readData, "Data read by filename should match what was written")

	// Try reading with a non-existent filename
	_, err = sm.ReadFile("nonexistent_file", 0, 10)
	assert.Error(t, err, "Reading from non-existent file should return an error")
}

func TestStorageManagerPersistence(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_persistence"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write some data
	data1 := []byte("initial data")
	err = sm.WriteFile(filename, data1, 0)
	require.NoError(t, err, "Failed to write initial data")

	// Seal the layer to simulate a checkpoint
	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Failed to commit layer")

	// Write more data
	data2 := []byte("more data")
	err = sm.WriteFile(filename, data2, uint64(len(data1)))
	require.NoError(t, err, "Failed to write more data")

	// Verify the data is correct
	fileSize, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent, err := sm.ReadFile(filename, 0, fileSize)
	require.NoError(t, err, "Failed to get full content")
	expectedContent := append(data1, data2...)
	assert.Equal(t, expectedContent, fullContent, "Full content should match expected")

	// Now create a new storage manager instance to simulate restarting the application
	sm2, cleanup2 := difffstest.SetupStorageManager(t)
	defer cleanup2()

	// Verify the data is still correct
	fileSize2, err := sm2.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent2, err := sm2.ReadFile(filename, 0, fileSize2)
	require.NoError(t, err, "Failed to get full content")
	assert.Equal(t, expectedContent, fullContent2, "Full content should persist across storage manager instances")

	// Verify we can still write to the file
	data3 := []byte("even more data")
	err = sm2.WriteFile(filename, data3, uint64(len(data1)+len(data2)))
	require.NoError(t, err, "Failed to write additional data")

	// Verify the combined data is correct
	fileSize3, err := sm2.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent3, err := sm2.ReadFile(filename, 0, fileSize3)
	require.NoError(t, err, "Failed to get full content")
	expectedContent3 := append(expectedContent, data3...)
	assert.Equal(t, expectedContent3, fullContent3, "Full content should include all writes")
}

func TestFuseScenario(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_fuse_scenario"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write some initial data
	initialData := []byte("initial data for FUSE test")
	err = sm.WriteFile(filename, initialData, 0)
	require.NoError(t, err, "Failed to write initial data")

	// Verify the data
	readData, err := sm.ReadFile(filename, 0, uint64(len(initialData)))
	require.NoError(t, err, "Failed to read data")
	assert.Equal(t, initialData, readData, "Read data should match written data")

	// Simulate a checkpoint
	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Failed to commit layer")

	// Write more data
	additionalData := []byte(" - additional data")
	err = sm.WriteFile(filename, additionalData, uint64(len(initialData)))
	require.NoError(t, err, "Failed to write additional data")

	// Read the combined data
	combinedData := append(initialData, additionalData...)
	readCombined, err := sm.ReadFile(filename, 0, uint64(len(combinedData)))
	require.NoError(t, err, "Failed to read combined data")
	assert.Equal(t, combinedData, readCombined, "Combined data should match expected")

	// Verify file size
	size, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	assert.Equal(t, uint64(len(combinedData)), size, "File size should match combined data length")

	// Create a new storage manager to simulate restarting
	sm2, cleanup2 := difffstest.SetupStorageManager(t)
	defer cleanup2()

	// Verify data persists
	readAfterRestart, err := sm2.ReadFile(filename, 0, uint64(len(combinedData)))
	require.NoError(t, err, "Failed to read data after restart")
	assert.Equal(t, combinedData, readAfterRestart, "Data should persist after restart")
}

func TestWriteBeyondFileSize(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_failed_write_beyond_file_size"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write data at different offsets
	err = sm.WriteFile(filename, []byte("first"), 0)
	require.NoError(t, err, "Failed to write 'first'")

	err = sm.WriteFile(filename, []byte("second"), 10)
	require.NoError(t, err, "Write should fail because it's beyond the file size")

	// check file content
	content, err := sm.ReadFile(filename, 0, 16)
	require.NoError(t, err, "Failed to read file content")
	assert.Equal(t, []byte("first\x00\x00\x00\x00\x00second"), content, "File content should match 'firstsecond'")
}

func TestCalculateVirtualFileSize(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_virtual_size"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write data at different offsets to create gaps
	writes := []struct {
		offset uint64
		data   []byte
	}{
		{0, []byte("start")},  // 0-4
		{5, []byte("middle")}, // 5-10
		{10, []byte("end")},   // 10-13
	}

	// Perform the writes
	for _, w := range writes {
		err = sm.WriteFile(filename, w.data, w.offset)
		require.NoError(t, err, "Failed to write at offset %d", w.offset)
	}

	// Get the file size
	size, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")

	// The size should be the highest offset + length of data at that offset
	expectedSize := uint64(10 + len([]byte("end")))
	assert.Equal(t, expectedSize, size, "File size should be based on highest offset + data length")

	// Seal the layer and write more data at a higher offset
	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Failed to commit layer")

	// Write at an even higher offset
	finalData := []byte("final")
	finalOffset := uint64(13)
	err = sm.WriteFile(filename, finalData, finalOffset)
	require.NoError(t, err, "Failed to write final data")

	// Get the updated file size
	newSize, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get updated file size")

	// The size should now be the new highest offset + length of data at that offset
	expectedNewSize := finalOffset + uint64(len(finalData))
	assert.Equal(t, expectedNewSize, newSize, "Updated file size should reflect the new highest offset + data length")
}

func TestDeleteFile(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create multiple test files
	filenames := []string{
		"testfile_delete_1",
		"testfile_delete_2",
		"testfile_delete_3",
	}

	// Insert all files
	for _, filename := range filenames {
		_, err := sm.InsertFile(filename)
		require.NoError(t, err, "Failed to insert file: %s", filename)

		// Write some data to each file
		err = sm.WriteFile(filename, []byte("data for "+filename), 0)
		require.NoError(t, err, "Failed to write to file: %s", filename)
	}

	// Get all files and verify count
	files, err := sm.GetAllFiles()
	require.NoError(t, err, "Failed to get all files")
	assert.Equal(t, len(filenames), len(files), "Should have the expected number of files")

	// Delete the second file
	err = sm.DeleteFile(filenames[1])
	require.NoError(t, err, "Failed to delete file")

	// Get all files again and verify count
	filesAfterDelete, err := sm.GetAllFiles()
	require.NoError(t, err, "Failed to get all files after delete")
	assert.Equal(t, len(filenames)-1, len(filesAfterDelete), "Should have one less file after deletion")

	// Verify the deleted file is gone
	var deletedFileFound bool
	for _, file := range filesAfterDelete {
		if file.Name == filenames[1] {
			deletedFileFound = true
			break
		}
	}
	assert.False(t, deletedFileFound, "Deleted file should not be found")

	// Verify the other files still exist
	var file1Found, file3Found bool
	for _, file := range filesAfterDelete {
		if file.Name == filenames[0] {
			file1Found = true
		}
		if file.Name == filenames[2] {
			file3Found = true
		}
	}
	assert.True(t, file1Found, "First file should still exist")
	assert.True(t, file3Found, "Third file should still exist")

	// Try to read from the deleted file
	_, err = sm.ReadFile(filenames[1], 0, 10)
	assert.Error(t, err, "Reading from deleted file should return an error")

	// Try to write to the deleted file
	err = sm.WriteFile(filenames[1], []byte("new data"), 0)
	assert.Error(t, err, "Writing to deleted file should return an error")
}

func TestExampleWorkflow(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_example_workflow"

	// Insert the file, which should create an initial active layer
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Load layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")
	require.Equal(t, 1, len(layers), "Expected one initial layer")

	// Write initial data.
	data1 := []byte("Hello, checkpoint!")
	err = sm.WriteFile(filename, data1, 0)
	require.NoError(t, err, "Failed to write initial data")

	// Simulate a checkpoint using our test instance.
	err = sm.Checkpoint(filename, "v1")
	require.NoError(t, err, "Failed to commit layer")

	// Write additional data.
	data2 := []byte("More data after checkpoint.")
	expectedOffset2 := uint64(len(data1))
	err = sm.WriteFile(filename, data2, expectedOffset2)
	require.NoError(t, err, "Failed to write additional data")

	// The full file content should be the concatenation of data1 and data2.
	expectedContent := append(data1, data2...)
	fileSize, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent, err := sm.ReadFile(filename, 0, fileSize)
	require.NoError(t, err, "Failed to get full content")
	assert.Equal(t, expectedContent, fullContent, "Full content should be the concatenation of data1 and data2")
}

func TestWriteToSameOffsetTwice(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_write_same_offset"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write initial data
	initialData := []byte("initial data")
	err = sm.WriteFile(filename, initialData, 0)
	require.NoError(t, err, "Failed to write initial data")

	// Verify the initial data was written correctly
	readData, err := sm.ReadFile(filename, 0, uint64(len(initialData)))
	require.NoError(t, err, "Failed to read initial data")
	assert.Equal(t, initialData, readData, "Initial data should be read correctly")

	// Write new data to the same offset
	newData := []byte("overwritten!")
	err = sm.WriteFile(filename, newData, 0)
	require.NoError(t, err, "Failed to write new data to the same offset")

	// Verify the new data overwrote the initial data
	readNewData, err := sm.ReadFile(filename, 0, uint64(len(newData)))
	require.NoError(t, err, "Failed to read new data")
	assert.Equal(t, newData, readNewData, "New data should overwrite initial data at the same offset")

	// Check the full content of the file
	fileSize, err := sm.SizeOf(filename)
	require.NoError(t, err, "Failed to get file size")
	fullContent, err := sm.ReadFile(filename, 0, fileSize)
	require.NoError(t, err, "Failed to get full content")
	assert.Equal(t, newData, fullContent, "Full content should match the new data")

	// Write data that partially overlaps with existing data
	partialData := []byte("partial")
	partialOffset := uint64(5) // This will overlap with part of the existing data
	err = sm.WriteFile(filename, partialData, partialOffset)
	require.NoError(t, err, "Failed to write partially overlapping data")

	// Expected content after partial write
	expectedContent := make([]byte, len(newData))
	copy(expectedContent, newData)
	// Overwrite the portion that should be replaced by partialData
	for i := range len(partialData) {
		if int(partialOffset)+i < len(expectedContent) {
			expectedContent[partialOffset+uint64(i)] = partialData[i]
		} else {
			expectedContent = append(expectedContent, partialData[i:]...)
			break
		}
	}

	// Verify the full content matches our expectations
	fullContentAfterPartial, err := sm.ReadFile(filename, 0, uint64(len(expectedContent)))
	require.NoError(t, err, "Failed to get full content")
	assert.Equal(t, expectedContent, fullContentAfterPartial, "Full content should reflect partial overwrite")
}

func TestVersionedLayers(t *testing.T) {
	// Setup a storage manager
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_versioned"
	fileID, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write some initial data
	initialData := []byte("initial data")
	err = sm.WriteFile(filename, initialData, 0)
	require.NoError(t, err, "Failed to write initial data")

	// Checkpoint with version tag "v1"
	versionTag1 := "v1"
	err = sm.Checkpoint(filename, versionTag1)
	require.NoError(t, err, "Failed to checkpoint file with version tag")

	// Write more data
	additionalData := []byte(" - additional data")
	err = sm.WriteFile(filename, additionalData, uint64(len(initialData)))
	require.NoError(t, err, "Failed to write additional data")

	// Checkpoint with version tag "v2"
	versionTag2 := "v2"
	err = sm.Checkpoint(filename, versionTag2)
	require.NoError(t, err, "Failed to checkpoint file with version tag")

	// Load all layers for the file
	layers, err := sm.LoadLayersByFileID(fileID)
	require.NoError(t, err, "Failed to load layers for file")
	require.Equal(t, 3, len(layers), "Expected three layers (initial, v1, v2)")

	db := difffstest.SetupDB(t)
	defer db.Close()

	// Get version IDs
	versionID1 := getVersionIDByTag(t, db, versionTag1)
	versionID2 := getVersionIDByTag(t, db, versionTag2)

	// Print actual values for debugging
	t.Logf("Layer 0 version ID: %d", layers[0].VersionID)
	t.Logf("Layer 1 version ID: %d", layers[1].VersionID)
	t.Logf("Layer 2 version ID: %d", layers[2].VersionID)
	t.Logf("Version ID for tag v1: %d", versionID1)
	t.Logf("Version ID for tag v2: %d", versionID2)

	// Check layer versions based on the actual values
	// First layer has version v1
	assert.Equal(t, versionTag1, layers[0].Tag, "First layer should have version v1")

	// Second layer has version v2
	assert.Equal(t, versionTag2, layers[1].Tag, "Second layer should have version v2")

	// Third layer has no version (it's the active layer)
	assert.Equal(t, "", layers[2].Tag, "Third layer should have no version (active layer)")

	// Verify that we can retrieve version tags by ID
	tag1 := getVersionTagByID(t, db, versionID1)
	assert.Equal(t, versionTag1, tag1, "Retrieved tag should match original tag")

	tag2 := getVersionTagByID(t, db, versionID2)
	assert.Equal(t, versionTag2, tag2, "Retrieved tag should match original tag")
}

func TestGetDataRangeWithVersion(t *testing.T) {
	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	// Create a test file
	filename := "testfile_versioned_read"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	// Write initial content
	initialContent := []byte("***************")
	err = sm.WriteFile(filename, initialContent, 0)
	require.NoError(t, err, "Failed to write initial content")

	// Create version v1
	v1Tag := "v1"
	err = sm.Checkpoint(filename, v1Tag)
	require.NoError(t, err, "Failed to checkpoint with version v1")

	// Write more content
	updatedContent := []byte("---------------")
	err = sm.WriteFile(filename, updatedContent, 0)
	require.NoError(t, err, "Failed to write updated content")

	// Create version v2
	v2Tag := "v2"
	err = sm.Checkpoint(filename, v2Tag)
	require.NoError(t, err, "Failed to checkpoint with version v2")

	// Write final content
	finalContent := []byte("@@@@@@@@@@@@@@@")
	err = sm.WriteFile(filename, finalContent, 0)
	require.NoError(t, err, "Failed to write final content")

	// Test reading with version v1
	v1Content, err := sm.ReadFile(filename, 0, 100, storage.WithVersion(v1Tag))
	require.NoError(t, err, "Failed to read content with version v1")
	assert.Equal(t, string(v1Content), string(initialContent), "Expected content for version v1 to be %q, got %q", initialContent, v1Content)

	// Test reading with version v2
	v2Content, err := sm.ReadFile(filename, 0, 100, storage.WithVersion(v2Tag))
	require.NoError(t, err, "Failed to read content with version v2")
	assert.Equal(t, string(v2Content), string(updatedContent), "Expected content for version v2 to be %q, got %q", updatedContent, v2Content)

	// Test reading latest content (no version specified)
	latestContent, err := sm.ReadFile(filename, 0, 100)
	require.NoError(t, err, "Failed to read latest content")

	assert.Equal(t, string(latestContent), string(finalContent), "Expected latest content to be %q, got %q", finalContent, latestContent)

	// Test reading with non-existent version
	_, err = sm.ReadFile(filename, 0, 100, storage.WithVersion("non_existent_version"))
	assert.Error(t, err, "Expected error when reading with non-existent version")
	assert.Contains(t, err.Error(), "version tag not found", "Error should indicate version tag not found")
}

func TestWithinAndOverlappingWrites(t *testing.T) {
	/**
	[2000, 4000):   	----------
	[1024, 2048):     @@@@@
	[3000, 6000):              %%%%%%%%%
	[0, 4096):   	***************
	*/

	sm, cleanup := difffstest.SetupStorageManager(t)
	defer cleanup()

	filename := "testfile_within_and_overlapping_writes"
	_, err := sm.InsertFile(filename)
	require.NoError(t, err, "Failed to insert file")

	first := make([]byte, 4096)
	// fill with *
	for i := range first {
		first[i] = '*'
	}

	err = sm.WriteFile(filename, first, 0)
	require.NoError(t, err, "Failed to write first data")

	{
		// check content
		content, err := sm.ReadFile(filename, 0, 4096)
		require.NoError(t, err, "Failed to read first data")
		assert.Equal(t, first, content, "Content should match")
	}

	second := make([]byte, 3000)
	// fill with %
	for i := range second {
		second[i] = '%'
	}

	err = sm.WriteFile(filename, second, 3000)
	require.NoError(t, err, "Failed to write second data")

	{
		// check content
		content, err := sm.ReadFile(filename, 0, 6000)
		require.NoError(t, err, "Failed to read first data")
		require.Equal(t, len(content), 6000, "Content should be 6000 bytes long")
		assert.Equal(t, string(content[:1024]), string(first[:1024]), "Bytes 0-1024 should match")
	}

	third := make([]byte, 1024)
	// fill with @
	for i := range third {
		third[i] = '@'
	}

	err = sm.WriteFile(filename, third, 1024)
	require.NoError(t, err, "Failed to write third data")

	{
		// check content
		content, err := sm.ReadFile(filename, 0, 6000)
		require.NoError(t, err, "Failed to read first data")
		require.Equal(t, len(content), 6000, "Content should be 6000 bytes long")
		assert.Equal(t, string(content[:1024]), string(first[:1024]), "Bytes 0-1024 should match")
		assert.Equal(t, string(content[1024:2048]), string(third), "Bytes 1024-2048 should match")
		assert.Equal(t, string(content[2048:3000]), string(first[:952]), "Bytes 2048-3000 should match")
		assert.Equal(t, string(content[3000:6000]), string(second), "Bytes 3000-6000 should match")
	}

	fourth := make([]byte, 2000)
	// fill with -
	for i := range fourth {
		fourth[i] = '-'
	}

	err = sm.WriteFile(filename, fourth, 2000)
	require.NoError(t, err, "Failed to write fourth data")

	{
		// check content
		content, err := sm.ReadFile(filename, 0, 6000)
		require.NoError(t, err, "Failed to read first data")
		require.Equal(t, len(content), 6000, "Content should be 6000 bytes long")

		// the final expected content should be:
		// [0, 1024): ****...
		// [1024, 2000): @@@@@...
		// [2000, 4000): ----------
		// [4000, 6000): %%%%...

		assert.Equal(t, string(content[:1024]), string(first[:1024]), "Bytes 0-1024 should match")
		assert.Equal(t, string(content[1024:2000]), string(third[:976]), "Bytes 1024-2000 should match")
		assert.Equal(t, string(content[2000:4000]), string(fourth[:2000]), "Bytes 2000-4000 should match")
		assert.Equal(t, string(content[4000:6000]), string(second[:2000]), "Bytes 4000-6000 should match")
	}
}

func getVersionIDByTag(t *testing.T, db *sql.DB, tag string) int64 {
	query := `SELECT id FROM versions WHERE tag = $1;`
	var versionID int64
	err := db.QueryRow(query, tag).Scan(&versionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0
		}
		t.Fatalf("Failed to get version ID for tag %s: %v", tag, err)
	}

	return versionID
}

func getVersionTagByID(t *testing.T, db *sql.DB, id int64) string {
	query := `SELECT tag FROM versions WHERE id = $1;`
	var tag string
	err := db.QueryRow(query, id).Scan(&tag)
	if err != nil {
		if err == sql.ErrNoRows {
			return ""
		}
		t.Fatalf("Failed to get version tag for ID %d: %v", id, err)
	}

	return tag
}
