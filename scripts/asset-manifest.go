//go:build ignore

// asset-manifest regenerates assets/manifest.json from the images under assets/game after a game update:
//
//	go run scripts/asset-manifest.go -client "Steam build 25950293" -resource 3.7.17
//
// Without flags it keeps the recorded game version. The manifest is the only input of the Public image pack, so review
// its diff before committing it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"wwlogs/internal/assets"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "asset-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	client := flag.String("client", "", "game client build the images were extracted from (default: keep the recorded value)")
	resource := flag.String("resource", "", "game resource version the images were extracted from (default: keep the recorded value)")
	flag.Parse()
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("cannot locate the repository")
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(sourcePath)), "assets")
	manifestPath := filepath.Join(root, "manifest.json")
	if *client == "" || *resource == "" {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return fmt.Errorf("no recorded game version; pass -client and -resource: %w", err)
		}
		recorded, err := assets.ParseManifest(data)
		if err != nil {
			return err
		}
		if *client == "" {
			*client = recorded.Client
		}
		if *resource == "" {
			*resource = recorded.Resource
		}
	}
	manifest, skipped, err := assets.ScanManifest(os.DirFS(root), *client, *resource)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, manifest.Encode(), 0o644); err != nil {
		return err
	}
	var total int64
	for _, entry := range manifest.Entries {
		total += entry.Size
	}
	fmt.Printf("assets/manifest.json: %d images, %d bytes\n", len(manifest.Entries), total)
	for _, path := range skipped {
		fmt.Printf("not a product image, left out: %s\n", path)
	}
	return nil
}
