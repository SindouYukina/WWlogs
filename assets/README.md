# Game images

`game/` holds the WebP images the product shows, addressed by static configuration ID through `internal/assets`. `manifest.json` lists every image with its SHA-256 and size. It is the only input of the Public image pack.

| Path | ID namespace | Route kind |
| --- | --- | --- |
| `game/characters/<RoleInfo.Id>/avatar.webp` | `RoleInfo.Id` | `character-avatar` |
| `game/characters/<RoleInfo.Id>/portrait.webp` | `RoleInfo.Id` | `character-portrait` |
| `game/skills/<SkillDescription.Id>/icon.webp` | `SkillDescription.Id` | `skill` |
| `game/skills/by-config/<Skill.Id>/icon.webp` | `Skill.Id` | `skill-config` |
| `game/echo-skills/<Vision.Id>/icon.webp` | `Vision.Id` (`PhantomSkill.PhantomSkillId`) | `echo-skill` |
| `game/buffs/<Buff.Id>/icon.webp` | `Buff.Id` | `buff` |

`scripts/build.ps1` (Public) verifies each listed image against the manifest, stores each distinct image once, and embeds the pack in the executable. A missing or changed image fails the build. Development reads the same tree only through an explicit `-asset-root assets`.

An ID without an image stays without one. It is never replaced with another ID's image.

## After a game update

1. Extract the images for the new client and place them under `game/` using the paths above. Reduce character portraits to a 1024-pixel longest edge before they enter this tree; the product applies no image filter at runtime.
2. Run `go run scripts/asset-manifest.go -client "<client build>" -resource "<resource version>"`.
3. Review the manifest diff, then build and verify.

These images are game material. Tracking them here records what the product uses and does not establish redistribution rights.
