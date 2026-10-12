package main

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
)

// ComputeMD5 calculates MD5 checksum in streaming chunks (memory safe).
// Note: MD5 is used exclusively for compatibility with Google Drive API v3 (file.Md5Checksum)
// to verify content integrity against Google Cloud hashes, not for cryptographic authentication.
func ComputeMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return computeMD5Reader(file)
}

func computeMD5Reader(reader io.Reader) (string, error) {
	hasher := md5.New()
	buf := make([]byte, 1024*1024) // 1MB buffer
	if _, err := io.CopyBuffer(hasher, reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
