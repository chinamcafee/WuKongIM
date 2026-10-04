import { ChevronRight } from "lucide-react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import type { ManagerApplicationLogEntry } from "@/lib/manager-api.types"
import { compactFieldLabel, displayLogLevel, importantLogFields, logLevelClassName, redactLogText } from "../log-format"
import { LogCopyButton } from "./log-copy-button"
import { LogHighlight } from "./log-highlight"

type AppLogRowProps = {
  entry: ManagerApplicationLogEntry
  keyword: string
  selected: boolean
  onSelect: () => void
}

export function AppLogRow({ entry, keyword, selected, onSelect }: AppLogRowProps) {
  const intl = useIntl()
  const message = redactLogText(entry.message || entry.raw)
  const error = typeof entry.fields?.error === "string" ? redactLogText(entry.fields.error) : ""
  // Bound summary text independently of the full event retained for details and copying.
  const summary = message.length > 500 ? message.slice(0, 500) + "…" : message
  const fields = importantLogFields(entry)
  const slot = entry.fields?.slot_id
  const level = displayLogLevel(entry.level) === "STACK"
    ? intl.formatMessage({ id: "appLogs.level.stack" }) : displayLogLevel(entry.level)

  return (
    <article className={`flex items-start gap-1 border-b border-white/5 px-3 py-2 last:border-b-0 hover:bg-white/[0.03] ${selected ? "bg-white/5" : ""}`} data-app-log-row="compact">
      <button
        aria-label={intl.formatMessage({ id: "appLogs.showDetailsAria" }, { seq: entry.seq })}
        aria-haspopup="dialog"
        className="grid min-w-0 flex-1 cursor-pointer grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-sm text-left outline-none focus-visible:ring-2 focus-visible:ring-sky-400 md:grid-cols-[10rem_4.25rem_minmax(0,1fr)]"
        onClick={onSelect}
        type="button"
      >
        <span className="whitespace-nowrap text-slate-400">{entry.time || "—"}</span>
        <span className={`w-fit self-start rounded border px-1.5 py-0.5 text-[10px] font-semibold leading-none ${logLevelClassName(entry.level)}`}>{level}</span>
        <span className="col-span-2 min-w-0 md:col-span-1">
          <span className="line-clamp-2 whitespace-pre-wrap break-all text-slate-100">
            {entry.module ? <span className="mr-2 text-slate-400">{entry.module}</span> : null}
            <LogHighlight text={summary} keyword={keyword} />
          </span>
          {error && error !== message ? (
            <span className="mt-1 line-clamp-2 whitespace-pre-wrap break-all text-xs text-amber-200/90" data-system-log-entry="error">
              <LogHighlight text={error.length > 500 ? error.slice(0, 500) + "…" : error} keyword={keyword} />
            </span>
          ) : null}
          {fields.length ? (
            <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 break-all text-[11px] text-slate-400" data-system-log-entry="metadata">
              {fields.map(([key, value]) => <span key={key}>{redactLogText(compactFieldLabel(key, value)).slice(0, 160)}</span>)}
            </span>
          ) : null}
        </span>
      </button>
      <div className="flex shrink-0 items-center gap-1">
        {slot !== undefined && slot !== null ? <Link className="hidden text-[11px] text-sky-300 hover:underline sm:inline" to={`/cluster/slots?tab=logs&slot_id=${encodeURIComponent(String(slot))}`}>{intl.formatMessage({ id: "appLogs.openSlot" }, { slot: String(slot) })}</Link> : null}
        <LogCopyButton compact label={intl.formatMessage({ id: "appLogs.copyMessageAria" }, { seq: entry.seq })} value={message} />
        <ChevronRight aria-hidden className="size-3 text-slate-500" />
      </div>
    </article>
  )
}
