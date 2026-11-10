package desktop

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"strings"
	"testing/fstest"
)

// missingFrontendPage is served when the executable was built without a frontend archive.
const missingFrontendPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>WWlogs</title></head>
<body style="font-family:sans-serif;background:#111;color:#eee;padding:24px">
<h1>WWlogs</h1><p>This executable was built without the frontend. Build it with scripts/build.ps1 -Target Desktop.</p>
</body></html>`

// frontendFS returns the embedded frontend, or a one-page fallback when none was embedded.
func frontendFS() (fs.FS, bool, error) {
	if frontendArchive == "" {
		return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(missingFrontendPage)}}, false, nil
	}
	reader, err := zip.NewReader(strings.NewReader(frontendArchive), int64(len(frontendArchive)))
	if err != nil {
		return nil, false, fmt.Errorf("open embedded frontend archive: %w", err)
	}
	if _, err := fs.Stat(reader, "index.html"); err != nil {
		return nil, false, fmt.Errorf("embedded frontend archive has no index.html: %w", err)
	}
	return reader, true, nil
}
