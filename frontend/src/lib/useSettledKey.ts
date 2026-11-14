import { useEffect, useState } from "react"

/** How long the refresh signal must stay unchanged before a History page queries the history again. */
export const historySettleMs = 1500

/**
 * The refresh signal once it has been still for delayMs. The engine's signal arrives as soon as an encounter finishes; its
 * history is written a moment later by its own writer (200 ms batches), so History queries follow the settled value.
 */
export function useSettledKey(key: string, delayMs = historySettleMs): string {
  const [settled, setSettled] = useState(key)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(key), delayMs)
    return () => clearTimeout(timer)
  }, [key, delayMs])
  return settled
}
