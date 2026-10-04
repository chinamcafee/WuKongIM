import { ArrowDown, Terminal } from "lucide-react"
import { useCallback, useLayoutEffect, useRef, useState } from "react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import type { ManagerApplicationLogEntry, ManagerApplicationLogSource } from "@/lib/manager-api.types"
import { basenameForLogSourceFile } from "../log-format"
import { AppLogDetails } from "./app-log-details"
import { AppLogRow } from "./app-log-row"

type AppLogConsoleProps = {
  title: string
  entries: ManagerApplicationLogEntry[]
  lineCount: string
  activeSource: ManagerApplicationLogSource | null
  source: string
  keyword: string
  followTail: boolean
  received: number
  revision: number
  refreshing: boolean
  rotated: boolean
  tailLimit: number
  maxTailLimit: number
  emptyDescription: string
  onHoldingChange: (holding: boolean) => void
  onLoadMore: () => void
}

function entryKey(entry: ManagerApplicationLogEntry) {
  return `${entry.offset}:${entry.seq}:${entry.raw}`
}

// Keeps scrolling separate from receiving. Reading and inspecting an event never follow new arrivals.
export function AppLogConsole(props: AppLogConsoleProps) {
  const intl = useIntl()
  const { onHoldingChange, entries } = props
  const scrollRef = useRef<HTMLDivElement>(null)
  const focusRef = useRef<HTMLElement | null>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [selected, setSelected] = useState<ManagerApplicationLogEntry | null>(null)
  const [acknowledged, setAcknowledged] = useState(0)
  const previous = useRef({ revision: props.revision, received: props.received, followTail: false, first: null as ManagerApplicationLogEntry | null, top: 0 })
  const following = props.followTail && atBottom && selected === null
  const unseen = Math.max(0, props.received - acknowledged)
  const position = selected ? props.entries.findIndex((entry) => entryKey(entry) === entryKey(selected)) : -1

  const rememberPosition = useCallback(() => {
    const viewport = scrollRef.current
    if (!viewport) return
    const viewportTop = viewport.getBoundingClientRect().top
    const visible = Array.from(viewport.querySelectorAll<HTMLElement>("[data-log-index]"))
      .find((row) => row.getBoundingClientRect().bottom > viewportTop)
    if (visible) {
      previous.current.first = entries[Number(visible.dataset.logIndex)] ?? null
      previous.current.top = visible.getBoundingClientRect().top - viewportTop
    }
  }, [entries])

  useLayoutEffect(() => {
    onHoldingChange(!atBottom || selected !== null)
  }, [atBottom, selected, onHoldingChange])

  useLayoutEffect(() => {
    const viewport = scrollRef.current
    if (!viewport) return
    const before = previous.current
    if (before.revision !== props.revision) {
      setAcknowledged(0)
      // Expanding a tail window prepends rows: retain the first visible event's screen position.
      const anchorIndex = before.first ? props.entries.findIndex((entry) => entryKey(entry) === entryKey(before.first!)) : -1
      const anchor = viewport.querySelector<HTMLElement>(`[data-log-index="${anchorIndex}"]`)
      if (anchor) viewport.scrollTop += anchor.getBoundingClientRect().top - viewport.getBoundingClientRect().top - before.top
    } else if (following && (before.received !== props.received || before.followTail !== props.followTail)) {
      viewport.scrollTop = viewport.scrollHeight
      setAcknowledged(props.received)
    }
    previous.current.revision = props.revision
    previous.current.received = props.received
    previous.current.followTail = props.followTail
    rememberPosition()
  }, [props.entries, props.received, props.revision, props.followTail, following, rememberPosition])


  function jumpToLatest() {
    const viewport = scrollRef.current
    if (viewport) viewport.scrollTop = viewport.scrollHeight
    setAtBottom(true)
    setAcknowledged(props.received)
    props.onHoldingChange(false)
    rememberPosition()
  }

  return (
    <>
      <section aria-label={props.title} className="overflow-hidden rounded-lg border border-[#242833] bg-[#0f1115] font-mono text-slate-100" data-system-log-console="terminal" aria-busy={props.refreshing}>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 bg-[#151923] px-3 py-2 text-xs">
          <div className="flex min-w-0 items-center gap-2">
            <Terminal aria-hidden className="size-3.5 shrink-0 text-emerald-300" />
            <span className="font-semibold">{intl.formatMessage({ id: "appLogs.console.title" })}</span>
            <span className="truncate text-slate-400">{props.activeSource ? basenameForLogSourceFile(props.activeSource.file) : props.source}</span>
          </div>
          <div className="flex items-center gap-3 text-[11px] text-slate-400">
            <span>{props.lineCount}</span>
            <span>{intl.formatMessage({ id: props.followTail ? following ? "appLogs.status.following" : "appLogs.status.reading" : "appLogs.status.paused" })}</span>
          </div>
        </div>
        {props.rotated ? <p className="border-b border-white/10 px-3 py-2 text-xs text-amber-200" role="status">{intl.formatMessage({ id: "appLogs.status.rotated" })}</p> : null}
        <div
          aria-label={intl.formatMessage({ id: "appLogs.console.title" })}
          aria-live="off"
          className="relative max-h-[min(56vh,560px)] min-h-48 overflow-auto overscroll-contain text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-sky-400 xl:max-h-[max(16rem,calc(100dvh-32rem))]"
          onScroll={() => {
            const viewport = scrollRef.current
            if (!viewport) return
            const bottom = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 24
            setAtBottom(bottom)
            props.onHoldingChange(!bottom || selected !== null)
            if (bottom) setAcknowledged(props.received)
            rememberPosition()
          }}
          ref={scrollRef}
          role="log"
          tabIndex={0}
        >
          {props.entries.length ? props.entries.map((entry, index) => (
            <div data-log-index={index} key={entryKey(entry)}>
              <AppLogRow entry={entry} keyword={props.keyword} selected={selected !== null && entryKey(selected) === entryKey(entry)} onSelect={() => {
                focusRef.current = document.activeElement as HTMLElement
                props.onHoldingChange(true)
                setAtBottom(false)
                setSelected(entry)
              }} />
            </div>
          )) : <p className="px-6 py-14 text-center font-sans text-sm text-slate-400">{props.emptyDescription}</p>}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-white/10 bg-[#151923] px-3 py-2">
          <div className="flex flex-wrap items-center gap-3">
            <Button className="border-white/15 bg-transparent text-slate-200 hover:bg-white/10 hover:text-white" disabled={props.refreshing || props.tailLimit >= props.maxTailLimit} onClick={props.onLoadMore} size="sm" type="button" variant="outline">
              {intl.formatMessage({ id: props.refreshing ? "common.refreshing" : props.tailLimit >= props.maxTailLimit ? "appLogs.windowMax" : "appLogs.expandWindow" })}
            </Button>
            <span className="text-[11px] text-slate-400">{intl.formatMessage({ id: "appLogs.windowSize" }, { count: props.tailLimit })}</span>
          </div>
          {!atBottom || unseen > 0 ? (
            <Button aria-label={intl.formatMessage({ id: "appLogs.jumpLatestAria" })} className="border-white/15 bg-transparent text-sky-200 hover:bg-white/10 hover:text-white" onClick={jumpToLatest} size="sm" type="button" variant="outline">
              <ArrowDown />{unseen > 0 ? intl.formatMessage({ id: "appLogs.liveLines" }, { count: unseen }) : intl.formatMessage({ id: "appLogs.jumpLatest" })}
            </Button>
          ) : null}
        </div>
      </section>
      <AppLogDetails
        entry={selected}
        onClose={() => setSelected(null)}
        onNext={() => { if (position >= 0 && position < props.entries.length - 1) setSelected(props.entries[position + 1]) }}
        onPrevious={() => { if (position > 0) setSelected(props.entries[position - 1]) }}
        onRestoreFocus={() => {
          if (focusRef.current?.isConnected) focusRef.current.focus({ preventScroll: true })
          else scrollRef.current?.focus({ preventScroll: true })
        }}
        position={position}
        total={props.entries.length}
      />
    </>
  )
}
