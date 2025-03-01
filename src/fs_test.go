package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuseReadWrite(t *testing.T) {
	// Create a temporary mount directory.
	mountDir, err := os.MkdirTemp("", "fusemnt")
	require.NoError(t, err, "TempDir error")
	defer os.RemoveAll(mountDir)

	// Build the FUSE filesystem binary (build from current directory).
	binaryPath := filepath.Join(os.TempDir(), "myfusefs_test")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	output, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "Build error: %s", output)
	defer os.Remove(binaryPath)

	// Set environment variables for the test process
	env := os.Environ()
	env = append(env, "POSTGRES_TEST_CONN="+GetTestConnectionString(t))
	env = append(env, "POSTGRES_HOST="+os.Getenv("POSTGRES_HOST"))
	env = append(env, "POSTGRES_PORT="+os.Getenv("POSTGRES_PORT"))
	env = append(env, "POSTGRES_USER="+os.Getenv("POSTGRES_USER"))
	env = append(env, "POSTGRES_PASSWORD="+os.Getenv("POSTGRES_PASSWORD"))
	env = append(env, "POSTGRES_DB="+os.Getenv("POSTGRES_DB"))

	// Start the FUSE filesystem process.
	cmd := exec.Command(binaryPath, "-mount", mountDir)
	cmd.Env = env
	require.NoError(t, cmd.Start(), "Failed to start FUSE FS")

	t.Log("mount dir " + mountDir)

	// Use our helper function to wait for the mount to be ready
	WaitForMount(mountDir, t)

	// Create the file first to ensure it exists
	filePath := filepath.Join(mountDir, "db.duckdb")
	createFile, err := os.Create(filePath)
	require.NoError(t, err, "Failed to create test file")
	require.NoError(t, createFile.Close(), "Failed to close file after creation")

	// Now open the file for read/write.
	f, err := os.OpenFile(filePath, os.O_RDWR, 0644)
	require.NoError(t, err, "Failed to open file %s", filePath)

	// Write some data.
	dataToWrite := []byte("test data")
	n, err := f.Write(dataToWrite)
	require.NoError(t, err, "Failed to write data")
	assert.Equal(t, len(dataToWrite), n, "Write should write all bytes")

	// Seek back to the beginning.
	_, err = f.Seek(0, 0)
	require.NoError(t, err, "Seek error")

	// Read the data back.
	readBuf := make([]byte, len(dataToWrite))
	n, err = f.Read(readBuf)
	require.NoError(t, err, "Failed to read data")
	assert.Equal(t, len(dataToWrite), n, "Read should return all bytes")
	assert.Equal(t, dataToWrite, readBuf, "Read data should match written data")

	// Close the file handle to ensure it's not busy.
	require.NoError(t, f.Close(), "Failed to close file")

	go func() {
		sigerr := cmd.Process.Signal(syscall.SIGINT)
		if sigerr != nil {
			t.Logf("Warning: Failed to send SIGINT: %v", sigerr)
		}
	}()

	// Wait for the process separately
	waiterr := cmd.Wait()
	if waiterr != nil && waiterr.Error() != "signal: interrupt" {
		require.NoError(t, waiterr, "FUSE process exited with an error")
	}
}
