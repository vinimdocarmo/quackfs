package main

// simulateCheckpoint seals the active layer of the provided LayerManager
// and logs a message. This simulates a DuckDB-like checkpoint.
func simulateCheckpoint(lm *LayerManager, filename string) {
	lm.SealActiveLayer(filename)
	Logger.Info("Checkpoint occurred: active layer sealed and new active layer created.")
}
