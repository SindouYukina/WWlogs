// Game images are addressed by static configuration ID. The host resolves these URLs through internal/assets, so the
// frontend never knows where images are stored. Runtime EntityIDs are not valid here.
export type GameAssetKind = "character-avatar" | "character-portrait" | "buff" | "skill" | "skill-config" | "echo-skill"

export function gameAssetUrl(kind: GameAssetKind, id: number): string {
  return `/game-assets/${kind}/${id}`
}

/** A failed image is either an ID with no image (404) or an image that could not be delivered (anything else). */
export type GameAssetFailure = "missing" | "failed"

// Most effect IDs have no icon, so the same answer is asked for repeatedly. The bound only limits a long session's growth.
const failureCacheLimit = 512
const failures = new Map<string, Promise<GameAssetFailure>>()

/** An <img> cannot read a response status, so a failed image asks once more and remembers the answer. */
export function classifyGameAssetFailure(url: string, request: (url: string) => Promise<{ status: number }> = fetch): Promise<GameAssetFailure> {
  let result = failures.get(url)
  if (!result) {
    result = request(url).then(response => (response.status === 404 ? "missing" as const : "failed" as const), () => "failed" as const)
    if (failures.size >= failureCacheLimit) failures.clear()
    failures.set(url, result)
  }
  return result
}
