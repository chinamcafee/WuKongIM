import { ChevronLeft, ChevronRight, X } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import type { ManagerApplicationLogEntry } from "@/lib/manager-api.types"
import { displayLogLevel, redactLogText } from "../log-format"
import { LogCopyButton } from "./log-copy-button"

type AppLogDetailsProps = {
  entry: ManagerApplicationLogEntry | null
  position: number
  total: number
  onClose: () => void
  onPrevious: () => void
  onNext: () => void
  onRestoreFocus: () => void
}

// Details retain one selected event while live rows arrive; navigation stays within the loaded window.
export function AppLogDetails({ entry, position, total, onClose, onPrevious, onNext, onRestoreFocus }: AppLogDetailsProps) {
  const intl = useIntl()
  const message = entry ? redactLogText(entry.message || entry.raw) : ""
  const fields = entry?.fields && Object.keys(entry.fields).length
    ? redactLogText(JSON.stringify(entry.fields, null, 2)) : ""

  return (
    <Sheet open={entry !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent
        className="gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-xl"
        showCloseButton={false}
        onCloseAutoFocus={(event) => { event.preventDefault(); onRestoreFocus() }}
      >
        <SheetHeader className="border-b border-border pr-14">
          <SheetTitle>{intl.formatMessage({ id: "appLogs.details.title" })}</SheetTitle>
          <SheetDescription>{position < 0 ? intl.formatMessage({ id: "appLogs.details.outsideWindow" }) : intl.formatMessage({ id: "appLogs.details.position" }, { position: position + 1, total })}</SheetDescription>
        </SheetHeader>
        <Button aria-label={intl.formatMessage({ id: "common.close" })} className="absolute right-3 top-3" onClick={onClose} size="icon-sm" variant="ghost"><X /></Button>
        {entry ? (
          <>
            <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
              <Button disabled={position <= 0} onClick={onPrevious} size="sm" variant="outline"><ChevronLeft />{intl.formatMessage({ id: "appLogs.details.previous" })}</Button>
              <Button disabled={position < 0 || position >= total - 1} onClick={onNext} size="sm" variant="outline">{intl.formatMessage({ id: "appLogs.details.next" })}<ChevronRight /></Button>
            </div>
            <div className="min-h-0 flex-1 space-y-6 overflow-y-auto p-4" key={entry.offset + ":" + entry.seq}>
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-2 text-sm">
                {[
                  ["appLogs.details.time", entry.time || "—"],
                  ["appLogs.level", displayLogLevel(entry.level)],
                  ["appLogs.details.module", entry.module || "—"],
                  ["appLogs.details.caller", redactLogText(entry.caller) || "—"],
                ].map(([id, value]) => (
                  <div className="contents" key={id}><dt className="text-muted-foreground">{intl.formatMessage({ id })}</dt><dd className="min-w-0 break-all font-mono text-xs leading-5">{value}</dd></div>
                ))}
              </dl>
              {entry.truncated ? <p className="rounded-md bg-amber-500/10 p-3 text-sm" role="status">{intl.formatMessage({ id: "appLogs.details.truncated" })}</p> : null}
              <section className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="font-medium">{intl.formatMessage({ id: "appLogs.details.message" })}</h3>
                  <LogCopyButton value={message} label={intl.formatMessage({ id: "appLogs.copyMessageAria" }, { seq: entry.seq })} />
                </div>
                <pre className="whitespace-pre-wrap break-all rounded-md bg-muted/50 p-3 font-mono text-xs leading-6">{message}</pre>
              </section>
              {fields ? (
                <section className="space-y-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <h3 className="font-medium">{intl.formatMessage({ id: "appLogs.details.fields" })}</h3>
                    <LogCopyButton value={fields} label={intl.formatMessage({ id: "appLogs.copyFields" })} />
                  </div>
                  <pre className="whitespace-pre-wrap break-all rounded-md bg-muted/50 p-3 font-mono text-xs leading-6">{fields}</pre>
                </section>
              ) : null}
              <section className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h3 className="font-medium">{intl.formatMessage({ id: "appLogs.details.raw" })}</h3>
                  <LogCopyButton value={entry.raw} label={intl.formatMessage({ id: "appLogs.copyRawAria" }, { seq: entry.seq })} />
                </div>
                <pre className="whitespace-pre-wrap break-all rounded-md bg-muted/50 p-3 font-mono text-xs leading-6" data-system-log-entry="raw">{redactLogText(entry.raw)}</pre>
              </section>
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
