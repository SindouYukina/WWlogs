package desktop

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/assets"
)

// gameAssetRoute is the URL prefix the frontend uses for images addressed by static configuration ID.
const gameAssetRoute = "/game-assets/"

// previewFontRoute serves Development comparison fonts from their separate, explicitly supplied directory.
const previewFontRoute = "/preview-fonts/"

var gameAssetKinds = map[string]assets.Kind{
	"character-avatar":   assets.CharacterAvatar,
	"character-portrait": assets.CharacterPortrait,
	"buff":               assets.BuffIcon,
	"skill":              assets.SkillIcon,
	"skill-config":       assets.SkillConfigIcon,
	"echo-skill":         assets.EchoSkillIcon,
}

// gameAssetSource selects where images come from: an explicitly supplied Development directory, else the pack embedded
// in the executable. Both feed the same resolver, so an ID means the same image either way. The host never searches the
// repository for a default directory. No source means every image reports as missing.
func gameAssetSource(root, pack string) fs.FS {
	if root != "" {
		return os.DirFS(root)
	}
	if pack != "" {
		return assets.NewPackFS(pack)
	}
	return nil
}

// withGameAssets serves images through the shared resolver and everything else through next. A route that names no image
// answers 404 and an image that cannot be read answers 500, so the UI can tell the two apart.
func withGameAssets(next http.Handler, source fs.FS) http.Handler {
	resolver := assets.NewResolver(source)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, gameAssetRoute) {
			next.ServeHTTP(response, request)
			return
		}
		kindName, idText, found := strings.Cut(strings.TrimPrefix(request.URL.Path, gameAssetRoute), "/")
		kind, known := gameAssetKinds[kindName]
		id, err := strconv.ParseUint(idText, 10, 64)
		if !found || !known || err != nil {
			http.NotFound(response, request)
			return
		}
		file, err := resolver.Open(assets.Key{Kind: kind, ID: id})
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, assets.ErrInvalidKey) {
			http.NotFound(response, request)
			return
		}
		if err != nil {
			http.Error(response, "game asset unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		response.Header().Set("Content-Type", "image/webp")
		response.Header().Set("Cache-Control", "max-age=3600")
		_, _ = io.Copy(response, file)
	})
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

var previewFontTypes = map[string]string{".json": "application/json", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf"}

// openShowcaseOverlays creates real frameless transparent windows for the Motion Showcase, so transparency, dragging and
// multi-window cost can be judged in WebView2. They run mock data only and have no path to the capture service.
func openShowcaseOverlays(app *application.App) {
	for kind, spec := range overlaySpecs {
		options := overlayWindowOptions(spec, "/?view=showcase&surface="+kind)
		options.Name = "showcase-" + kind
		options.Hidden = false
		options.InitialPosition = application.WindowXY
		options.X, options.Y = spec.offsetX, spec.offsetY
		app.Window.NewWithOptions(options)
	}
}
