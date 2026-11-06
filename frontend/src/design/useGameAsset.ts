import { useCallback, useRef, useState } from "react"
import { classifyGameAssetFailure, type GameAssetFailure } from "./assets"

export type GameAssetState = "loading" | "loaded" | GameAssetFailure

/** Tracks one <img>. The state follows the URL, so a previous ID's result can never describe the current image. */
export function useGameAsset(url: string | undefined) {
  const [result, setResult] = useState<{ url: string | undefined; state: GameAssetState }>({ url, state: "loading" })
  const current = useRef(url)
  current.current = url
  const onLoad = useCallback(() => setResult({ url, state: "loaded" }), [url])
  const onError = useCallback(() => {
    if (url === undefined) return
    void classifyGameAssetFailure(url).then(state => {
      if (current.current === url) setResult({ url, state })
    })
  }, [url])
  const state: GameAssetState = result.url === url ? result.state : "loading"
  return { state, onLoad, onError }
}
