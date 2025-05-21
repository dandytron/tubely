package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func mediaTypeToExt(mediaType string) string {
	parts := strings.Split(mediaType, "/")
	if len(parts) != 2 {
		return ".bin"
	}
	return "." + parts[1]
}

// Use the crypto/rand.Read to create a unique file path. filepath.Join and cfg.assetsRoot will be helpful here.
// fill a 32-byte slice with random bytes. Use base64.RawURLEncoding to then convert it into a random base64 string.
// Use this string as the file name, and set the extension based on the media type (same as before)
func getAssetPath(mediaType string) string {
	byteSlice := make([]byte, 32)
	_, err := rand.Read(byteSlice)
	if err != nil {
		panic("failed to generate random bytes")
	}

	id := base64.RawURLEncoding.EncodeToString(byteSlice)

	ext := mediaTypeToExt(mediaType)
	return fmt.Sprintf("%s%s", id, ext)
}

func (cfg apiConfig) getAssetDiskPath(assetPath string) string {
	return filepath.Join(cfg.assetsRoot, assetPath)
}

func (cfg apiConfig) getAssetURL(assetPath string) string {
	return fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, assetPath)
}
