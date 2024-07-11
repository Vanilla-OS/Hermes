package utils

import (
	"log"
	"os"
	"path/filepath"
)

func GetBuildsPath(root, codename string) string {
	return filepath.Join(root, codename)
}

func CreateDir(path string) {
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		log.Fatalf("error creating directory %s: %v", path, err)
	}
}

func CreateSymlink(target, symlink string) {
	if err := os.Remove(symlink); err != nil && !os.IsNotExist(err) {
		log.Fatalf("error removing existing symlink %s: %v", symlink, err)
	}
	if err := os.Symlink(target, symlink); err != nil {
		log.Fatalf("error creating symlink %s -> %s: %v", symlink, target, err)
	}
	log.Printf("symlink created: %s -> %s", symlink, target)
}
