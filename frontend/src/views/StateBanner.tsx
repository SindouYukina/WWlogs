import { Badge } from "@/components/ui/badge"
import { useTranslation } from "react-i18next"
import type { Connection, DataState, Snapshot } from "@/lib/snapshot"

export function StateBanner({connection,state,snapshot,stale=false}:{connection:Connection;state:DataState;snapshot:Snapshot|null;stale?:boolean}) {
  const {t}=useTranslation("common")
  let text=t("live"),variant:"outline"|"secondary"|"destructive"="outline"
  if(connection==="disconnected"){text=t("disconnected");variant="destructive"}
  else if(connection==="connecting"){text=t("connecting");variant="secondary"}
  else if(stale){text=t("stale");variant="destructive"}
  else if(snapshot?.service.state==="error"){text=t("captureFailed");variant="destructive"}
  else if(state==="not_capturing"){text=t("notCapturing");variant="secondary"}
  else if(state==="no_data")text=t("waiting")
  else if(state==="no_encounter")text=t("noEncounter")
  else if(state==="untrusted"){text=t("untrusted");variant="destructive"}
  else if(state==="degraded")text=t("degraded")
  return <Badge variant={variant} className="whitespace-normal text-left" data-field="banner">{text}</Badge>
}
