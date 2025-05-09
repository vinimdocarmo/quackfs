// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.28.0

package sqlc

import (
	"context"
)

type Querier interface {
	CalcFileSize(ctx context.Context, fileID uint64) (int64, error)
	GetAllFiles(ctx context.Context) ([]File, error)
	GetFileIDByName(ctx context.Context, name string) (uint64, error)
	GetLayerByVersion(ctx context.Context, arg GetLayerByVersionParams) (GetLayerByVersionRow, error)
	GetLayerChunks(ctx context.Context, snapshotLayerID uint64) ([]GetLayerChunksRow, error)
	GetLayersByFileID(ctx context.Context, fileID uint64) ([]GetLayersByFileIDRow, error)
	GetObjectKey(ctx context.Context, id uint64) (string, error)
	GetOverlappingChunksWithVersion(ctx context.Context, arg GetOverlappingChunksWithVersionParams) ([]GetOverlappingChunksWithVersionRow, error)
	InsertChunk(ctx context.Context, arg InsertChunkParams) error
	InsertFile(ctx context.Context, name string) (uint64, error)
	InsertLayer(ctx context.Context, arg InsertLayerParams) (uint64, error)
	InsertVersion(ctx context.Context, tag string) (uint64, error)
}

var _ Querier = (*Queries)(nil)
