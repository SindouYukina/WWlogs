import { useEffect, useState } from "react"

/**
 * Whether bars mounting now belong to the opening of `key` and so play the entrance (DamageBar enter). It is true from the
 * first render of a new key until the first commit in which its content is ready, and false afterwards. A bar decides its
 * entrance when it mounts and plays it to the end, so closing the window right after that commit never cuts a growing bar
 * short, while bars that mount later, after a tab switch or a filter change, appear at their value.
 */
export function useEntrance(key: string | number, ready: boolean): boolean {
  const [open, setOpen] = useState<string | number | null>(key)
  const [seen, setSeen] = useState(key)
  if (seen !== key) {
    setSeen(key)
    setOpen(key)
  }
  useEffect(() => {
    if (ready) setOpen(current => current === key ? null : current)
  }, [key, ready])
  return open === key
}
