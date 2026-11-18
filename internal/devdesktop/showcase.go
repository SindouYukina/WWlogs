package devdesktop

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/desktop"
)

// showcaseRoute is the frontend route of the Development Motion Showcase (mock data only).
const showcaseRoute = "/?view=showcase"

// previewFontRoute serves Development comparison fonts from their separate, explicitly supplied directory.
const previewFontRoute = "/preview-fonts/"

var previewFontTypes = map[string]string{".json": "application/json", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf"}

// showcaseSurface is the Motion Showcase as the host's review surface: its window, its transparent Overlay windows when
// overlays is set, and the font candidates of fontRoot when one is given. warnings receives the one notice of a font root
// that is not a directory.
func showcaseSurface(overlays bool, fontRoot string, warnings io.Writer) *desktop.ReviewSurface {
	surface := &desktop.ReviewSurface{Route: showcaseRoute}
	if overlays {
		surface.Open = openShowcaseOverlays
	}
	if fontRoot != "" {
		surface.Assets = func(next http.Handler) http.Handler {
			// A wrong path is reported here and shows as Missing in the Showcase; it never stops the application.
			if info, err := os.Stat(fontRoot); err != nil || !info.IsDir() {
				fmt.Fprintln(warnings, "WWlogs desktop: the -font-root directory was not found; font candidates will show as missing")
			}
			return withPreviewFonts(next, os.DirFS(fontRoot))
		}
	}
	return surface
}

// openShowcaseOverlays creates real frameless transparent windows for the Motion Showcase, so transparency, dragging and
// multi-window cost can be judged in WebView2. They run mock data only and have no path to the capture service.
func openShowcaseOverlays(app *application.App) {
	windows := desktop.ReviewOverlayWindows(func(kind string) string { return showcaseRoute + "&surface=" + kind })
	for kind, options := range windows {
		options.Name = "showcase-" + kind
		app.Window.NewWithOptions(options)
	}
}

// withPreviewFonts serves only candidate font files from the separate, explicitly supplied directory.
func withPreviewFonts(next http.Handler, source fs.FS) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		name, isFont := strings.CutPrefix(request.URL.Path, previewFontRoute)
		if !isFont {
			next.ServeHTTP(response, request)
			return
		}
		servePreviewFont(response, request, source, name)
	})
}

// servePreviewFont serves only font files and the manifest. fs.ValidPath rejects any path escape.
func servePreviewFont(response http.ResponseWriter, request *http.Request, source fs.FS, name string) {
	contentType, known := previewFontTypes[path.Ext(name)]
	if !known || !fs.ValidPath(name) {
		http.NotFound(response, request)
		return
	}
	file, err := source.Open(name)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer file.Close()
	response.Header().Set("Content-Type", contentType)
	_, _ = io.Copy(response, file)
}
