import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Copy, Search } from "lucide-react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import type { ManagerNodeConfigDocument } from "@/lib/manager-api.types"
import { configDocumentLines, configDocumentText } from "@/lib/node-config-document"
import { nodeConfigGroupTitle } from "@/lib/node-config-i18n"
import { cn } from "@/lib/utils"

export function NodeConfigDocumentView({ document }: { document: ManagerNodeConfigDocument }) {
  const intl = useIntl()
  const [query, setQuery] = useState("")
  const [showHelp, setShowHelp] = useState(false)
  const [matchIndex, setMatchIndex] = useState(0)
  const [copyResult, setCopyResult] = useState<{ text: string; status: "copied" | "error" } | null>(null)
  const lineRefs = useRef(new Map<number, HTMLSpanElement>())
  const lines = useMemo(() => configDocumentLines(document, intl, showHelp), [document, intl, showHelp])
  const text = useMemo(() => configDocumentText(lines), [lines])
  const copyState = copyResult?.text === text ? copyResult.status : "idle"
  const normalizedQuery = query.trim().toLowerCase()
  const matches = useMemo(
    () => normalizedQuery
      ? lines.filter((line) => !line.annotation && line.searchText.includes(normalizedQuery)).map((line) => line.baseLine)
      : [],
    [lines, normalizedQuery],
  )
  const activeLine = matches[matchIndex % (matches.length || 1)]

  useEffect(() => {
    if (activeLine) lineRefs.current.get(activeLine)?.scrollIntoView?.({ block: "center" })
  }, [activeLine, normalizedQuery])

  async function copy() {
    try {
      if (!navigator.clipboard) throw new Error("clipboard unavailable")
      await navigator.clipboard.writeText(text)
      setCopyResult({ text, status: "copied" })
    } catch {
      setCopyResult({ text, status: "error" })
    }
  }

  return (
    <div className="min-w-0 space-y-3">
      <p className="text-xs text-muted-foreground">{intl.formatMessage({ id: "nodeConfig.toml.notice" })}</p>
      <div className="flex flex-wrap items-center gap-2">
        <label className="relative min-w-40 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            aria-label={intl.formatMessage({ id: "nodeConfig.config.search" })}
            className="h-8 w-full rounded-md border border-input bg-background px-3 pl-8 font-mono text-xs outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
            onChange={(event) => { setQuery(event.target.value); setMatchIndex(0) }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && matches.length) {
                event.preventDefault()
                setMatchIndex((current) => (current + (event.shiftKey ? -1 : 1) + matches.length) % matches.length)
              }
            }}
            value={query}
          />
        </label>
        <span aria-live="polite" className="min-w-12 font-mono text-xs text-muted-foreground">
          {normalizedQuery ? intl.formatMessage({ id: "nodeConfig.toml.matches" }, {
            current: matches.length ? matchIndex % matches.length + 1 : 0, total: matches.length,
          }) : intl.formatMessage({ id: "nodeConfig.toml.fields" }, { count: document.fields.length })}
        </span>
        <Button aria-label={intl.formatMessage({ id: "nodeConfig.toml.previous" })} disabled={!matches.length}
          onClick={() => setMatchIndex((current) => (current - 1 + matches.length) % matches.length)} size="sm" variant="ghost">
          <ArrowUp className="size-4" />
        </Button>
        <Button aria-label={intl.formatMessage({ id: "nodeConfig.toml.next" })} disabled={!matches.length}
          onClick={() => setMatchIndex((current) => (current + 1) % matches.length)} size="sm" variant="ghost">
          <ArrowDown className="size-4" />
        </Button>
        <label className="flex cursor-pointer items-center gap-2 text-xs">
          <input checked={showHelp} onChange={(event) => setShowHelp(event.target.checked)} type="checkbox" />
          {intl.formatMessage({ id: "nodeConfig.toml.showHelp" })}
        </label>
        <Button onClick={() => void copy()} size="sm" variant="outline">
          <Copy className="mr-2 size-4" />{intl.formatMessage({ id: "nodeConfig.toml.copy" })}
        </Button>
        {copyState !== "idle" ? <span role="status" className="text-xs text-muted-foreground">
          {intl.formatMessage({ id: copyState === "copied" ? "nodeConfig.copied" : "nodeConfig.toml.copyError" })}
        </span> : null}
      </div>
      <nav aria-label={intl.formatMessage({ id: "nodeConfig.groups" })} className="flex flex-wrap gap-1 border-b border-border pb-2">
        {document.sections.map((section) => (
          <button className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
            key={section.path} onClick={() => lineRefs.current.get(section.line)?.scrollIntoView?.({ block: "start" })} type="button">
            {nodeConfigGroupTitle(intl, { id: section.path, title: section.path })}
          </button>
        ))}
      </nav>
      <pre aria-label={intl.formatMessage({ id: "nodeConfig.toml.document" })} tabIndex={0}
        className="max-h-[68vh] overflow-auto rounded-md border border-border bg-background py-3 font-mono text-xs leading-6 outline-none focus:ring-2 focus:ring-ring/30">
        <code>
          {lines.map((line, index) => (
            <span key={index}
              ref={line.annotation ? undefined : (element) => {
                if (element) lineRefs.current.set(line.baseLine, element)
                else lineRefs.current.delete(line.baseLine)
              }}
              data-line={line.annotation ? undefined : line.baseLine}
              data-active={line.baseLine === activeLine && !line.annotation ? "true" : undefined}
              className={cn("block min-w-full w-max scroll-mt-3 pr-4",
                line.baseLine === activeLine && !line.annotation && "bg-primary/10",
                line.text.startsWith("#") && "text-muted-foreground")}
            >
              <span aria-hidden="true" className="inline-block w-12 select-none pr-3 text-right text-muted-foreground/60">{index + 1}</span>
              <TomlLine text={line.text} query={normalizedQuery} />
              {"\n"}
            </span>
          ))}
        </code>
      </pre>
    </div>
  )
}

function TomlLine({ text, query }: { text: string; query: string }) {
  if (text.startsWith("#")) return <Highlight text={text} query={query} />
  if (text.startsWith("[")) return <span className="font-semibold text-primary"><Highlight text={text} query={query} /></span>
  const equals = text.indexOf("=")
  if (equals < 0) return <Highlight text={text} query={query} />
  return <><span className="text-foreground"><Highlight text={text.slice(0, equals)} query={query} /></span>
    <span className="text-muted-foreground">=</span>
    <span className="text-[var(--status-healthy)]"><Highlight text={text.slice(equals + 1)} query={query} /></span></>
}

function Highlight({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>
  const parts = []
  let cursor = 0
  let index = text.toLowerCase().indexOf(query)
  while (index !== -1) {
    parts.push(text.slice(cursor, index))
    parts.push(<mark className="rounded-sm bg-amber-200 text-slate-950" key={index}>{text.slice(index, index + query.length)}</mark>)
    cursor = index + query.length
    index = text.toLowerCase().indexOf(query, cursor)
  }
  parts.push(text.slice(cursor))
  return <>{parts}</>
}
