package assets

import (
	"errors"
	"io/fs"
	"strconv"
)

// Kind identifies both an image's purpose and the configuration ID namespace.
type Kind uint8

const (
	CharacterAvatar   Kind = iota + 1 // RoleInfo.Id
	CharacterPortrait                 // RoleInfo.Id
	SkinAvatar                        // RoleSkin.Id, scoped to RoleInfo.Id
	SkinPortrait                      // RoleSkin.Id, scoped to RoleInfo.Id
	SkillIcon                         // SkillDescription.Id
	BuffIcon                          // Buff.Id
	EchoIcon                          // PhantomItem.Id
	WeaponIcon                        // WeaponConf.Id
)

// Key selects an image using static configuration IDs. Entity IDs are not supported.
type Key struct {
	Kind   Kind
	ID     uint64
	SkinID uint64 // Required only for skin images.
}

// Resolver reads WebP assets from the root of a caller-supplied filesystem.
// The same resolver can use a reviewed embedded filesystem or a local preview filesystem.
type Resolver struct {
	source fs.FS
}

var ErrInvalidKey = errors.New("invalid game asset key")

// NewResolver uses source as the root of the game asset tree.
func NewResolver(source fs.FS) Resolver {
	return Resolver{source: source}
}

// Open returns an image or an error matching fs.ErrNotExist when it is missing.
// The caller must close the returned file.
func (r Resolver) Open(key Key) (fs.File, error) {
	path, err := key.imagePath()
	if err != nil {
		return nil, err
	}
	if r.source == nil {
		return nil, fs.ErrNotExist
	}
	return r.source.Open(path)
}

func (key Key) imagePath() (string, error) {
	skin := key.Kind == SkinAvatar || key.Kind == SkinPortrait
	if key.ID == 0 || skin != (key.SkinID != 0) {
		return "", ErrInvalidKey
	}
	id := strconv.FormatUint(key.ID, 10)
	switch key.Kind {
	case CharacterAvatar:
		return "game/characters/" + id + "/avatar.webp", nil
	case CharacterPortrait:
		return "game/characters/" + id + "/portrait.webp", nil
	case SkinAvatar, SkinPortrait:
		variant := "game/characters/" + id + "/variants/" + strconv.FormatUint(key.SkinID, 10)
		if key.Kind == SkinAvatar {
			return variant + "/avatar.webp", nil
		}
		return variant + "/portrait.webp", nil
	case SkillIcon:
		return "game/skills/" + id + "/icon.webp", nil
	case BuffIcon:
		return "game/buffs/" + id + "/icon.webp", nil
	case EchoIcon:
		return "game/echoes/" + id + "/icon.webp", nil
	case WeaponIcon:
		return "game/weapons/" + id + "/icon.webp", nil
	default:
		return "", ErrInvalidKey
	}
}
