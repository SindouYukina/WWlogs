package assets_test

import (
	"errors"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"wwlogs/internal/assets"
)

func TestResolverUsesConfigurationNamespace(t *testing.T) {
	resolver := assets.NewResolver(fstest.MapFS{
		"game/characters/98765/avatar.webp":              {Data: []byte("new role")},
		"game/characters/98765/portrait.webp":            {Data: []byte("portrait")},
		"game/characters/98765/variants/123/avatar.webp": {Data: []byte("skin")},
		"game/skills/98765/icon.webp":                    {Data: []byte("skill")},
	})
	for _, test := range []struct {
		key  assets.Key
		want string
	}{
		{assets.Key{Kind: assets.CharacterAvatar, ID: 98765}, "new role"},
		{assets.Key{Kind: assets.CharacterPortrait, ID: 98765}, "portrait"},
		{assets.Key{Kind: assets.SkinAvatar, ID: 98765, SkinID: 123}, "skin"},
		{assets.Key{Kind: assets.SkillIcon, ID: 98765}, "skill"},
	} {
		file, err := resolver.Open(test.key)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || string(data) != test.want {
			t.Fatalf("key %+v: got %q, read error %v, close error %v", test.key, data, readErr, closeErr)
		}
	}

	for _, test := range []struct {
		key  assets.Key
		want error
	}{
		{assets.Key{Kind: assets.BuffIcon, ID: 98765}, fs.ErrNotExist},
		{assets.Key{Kind: assets.SkinAvatar, ID: 98765, SkinID: 124}, fs.ErrNotExist},
		{assets.Key{Kind: assets.SkinAvatar, ID: 98765}, assets.ErrInvalidKey},
		{assets.Key{Kind: assets.CharacterAvatar, ID: 98765, SkinID: 123}, assets.ErrInvalidKey},
		{assets.Key{Kind: assets.CharacterAvatar}, assets.ErrInvalidKey},
		{assets.Key{ID: 98765}, assets.ErrInvalidKey},
	} {
		if file, err := resolver.Open(test.key); !errors.Is(err, test.want) {
			if file != nil {
				file.Close()
			}
			t.Fatalf("key %+v: got %v, want %v", test.key, err, test.want)
		}
	}
}
