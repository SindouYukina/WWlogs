import { useCallback, useEffect, useState } from "react"
import type { DetailSelection } from "./combat-view"

export type DetailPick = readonly [DetailSelection | null, (key: string) => void]

/**
 * The user's ranking pick for one encounter (or capture interval) scope. A new scope ends the pick for good, even if an
 * earlier identity is shown again later, so a pick never comes back in another encounter. The holder decides how long it
 * lives: the Desktop keeps it across page switches, a Dashboard without a holder keeps its own.
 */
export function useDetailPick(scope: string): DetailPick {
  const [pick, setPick] = useState<DetailSelection | null>(null)
  useEffect(() => { setPick(current => current && current.scope !== scope ? null : current) }, [scope])
  const choose = useCallback((key: string) => setPick({ scope, key }), [scope])
  return [pick, choose]
}
