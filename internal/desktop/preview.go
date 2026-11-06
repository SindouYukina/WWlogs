package desktop

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"wwlogs/internal/assets"
)

// gameAssetRoute is the URL prefix the frontend uses for images addressed by static configuration ID.
const gameAssetRoute = "/game-assets/"

var gameAssetKinds = map[string]assets.Kind{
	"character-avatar":   assets.CharacterAvatar,
	"character-portrait": assets.CharacterPortrait,
	"buff":               assets.BuffIcon,
	"skill":              assets.SkillIcon,
}

// withGameAssets serves images through the shared resolver and everything else through next. The source is a directory
// named on the command line; the host has no default location and never looks inside the repository on its own.
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

// openShowcaseOverlays creates real frameless transparent windows for the Motion Showcase, so transparency, dragging and
// multi-window cost can be judged in WebView2. They run mock data only and have no path to the capture service.
func openShowcaseOverlays(app *application.App) {
	for _, surface := range []struct {
		name          string
		width, height int
		x, y          int
	}{
		{name: "combat", width: 486, height: 268, x: 60, y: 60},
		{name: "effects", width: 320, height: 420, x: 60, y: 350},
	} {
		app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:            "showcase-" + surface.name,
			Title:           "WWlogs Showcase " + surface.name,
			Width:           surface.width,
			Height:          surface.height,
			InitialPosition: application.WindowXY,
			X:               surface.x,
			Y:               surface.y,
			URL:             "/?view=showcase&surface=" + surface.name,
			Frameless:       true,
			AlwaysOnTop:     true,
			BackgroundType:  application.BackgroundTypeTransparent,
			Windows:         application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true},
		})
	}
}
