// Game images are addressed by static configuration ID. The host resolves these URLs through internal/assets, so the
// frontend never knows where images are stored. Runtime EntityIDs are not valid here.
export type GameAssetKind = "character-avatar" | "character-portrait" | "buff" | "skill"

export function gameAssetUrl(kind: GameAssetKind, id: number): string {
  return `/game-assets/${kind}/${id}`
}
