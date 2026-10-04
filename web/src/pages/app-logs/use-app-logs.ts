import { useCallback, useEffect, useRef, useState } from "react"

import { defaultNodeId } from "@/components/manager/node-filter"
import {
  getApplicationLogEntries, getApplicationLogSources, getNodes, streamApplicationLogEntries,
} from "@/lib/manager-api"
import type {
  ManagerApplicationLogEntry, ManagerApplicationLogSource, ManagerApplicationLogStreamEvent,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"
import { levelsForSeverityFilter, type AppLogSeverityFilter } from "./log-format"

export const appLogPageLimit = 100
export const appLogMaxTailLimit = 1000
const livePollIntervalMs = 2000

type Query = {
  nodeId: number | null
  source: string
  keyword: string
  severity: AppLogSeverityFilter
  limit: number
}

type LogData = {
  key: string
  entries: ManagerApplicationLogEntry[]
  cursor: string
  rotated: boolean
  received: number
  revision: number
}

function asError(error: unknown) {
  return error instanceof Error ? error : new Error("Application log request failed")
}

// Owns bounded log reads. Each effect discards responses after its query is superseded.
export function useAppLogs() {
  const [query, setQuery] = useState<Query>(() => ({
    nodeId: null, source: "app",
    keyword: new URLSearchParams(window.location.search).get("keyword")?.trim() ?? "",
    severity: "", limit: appLogPageLimit,
  }))
  const [nodes, setNodes] = useState<ManagerNodesResponse | null>(null)
  const [nodeError, setNodeError] = useState<Error | null>(null)
  const [nodeRetry, setNodeRetry] = useState(0)
  const [sourceRetry, setSourceRetry] = useState(0)
  const [refresh, setRefresh] = useState(0)
  const [sourceState, setSourceState] = useState<{
    nodeId: number | null; items: ManagerApplicationLogSource[]; error: Error | null
  }>({ nodeId: null, items: [], error: null })
  const [readState, setReadState] = useState({ key: "", pending: false, error: null as Error | null })
  const [data, setData] = useState<LogData>({
    key: "", entries: [], cursor: "", rotated: false, received: 0, revision: 0,
  })
  // Polling reads the last accepted cursor synchronously, never a previous render's cursor.
  const dataRef = useRef(data)
  const holdingPosition = useRef(false)
  const [followTail, setFollowTail] = useState(false)
  const [liveStatus, setLiveStatus] = useState<"idle" | "live" | "heartbeat" | "error" | "bufferFull">("idle")

  const acceptData = useCallback((next: LogData) => {
    dataRef.current = next
    setData(next)
  }, [])

  useEffect(() => {
    let cancelled = false
    void getNodes().then((result) => {
      if (cancelled) return
      setNodes(result)
      setNodeError(null)
      setQuery((current) => ({ ...current, nodeId: defaultNodeId(result) }))
    }).catch((error: unknown) => {
      if (!cancelled) setNodeError(asError(error))
    })
    return () => { cancelled = true }
  }, [nodeRetry])

  useEffect(() => {
    if (query.nodeId === null) return
    const nodeId = query.nodeId
    let cancelled = false
    void getApplicationLogSources(nodeId).then((result) => {
      if (!cancelled) setSourceState({ nodeId, items: result.sources, error: null })
    }).catch((error: unknown) => {
      if (!cancelled) setSourceState({ nodeId, items: [], error: asError(error) })
    })
    return () => { cancelled = true }
  }, [query.nodeId, sourceRetry])

  const sourcesReady = query.nodeId !== null && sourceState.nodeId === query.nodeId
  const sources = sourcesReady ? sourceState.items : []
  const activeSource = sources.find((item) => item.name === query.source && item.available)
    ?? sources.find((item) => item.available) ?? null
  const source = activeSource?.name ?? ""
  const key = JSON.stringify([query.nodeId, source, query.keyword, query.severity])
  const requestKey = JSON.stringify([key, query.limit, refresh])
  const canLoad = query.nodeId !== null && activeSource !== null

  useEffect(() => {
    if (!canLoad || query.nodeId === null) return
    let cancelled = false
    void getApplicationLogEntries({
      nodeId: query.nodeId, source, limit: query.limit,
      keyword: query.keyword, levels: levelsForSeverityFilter(query.severity),
    }).then((page) => {
      if (cancelled) return
      acceptData({
        key, entries: page.items.slice(-appLogMaxTailLimit), cursor: page.cursor,
        rotated: page.rotated, received: 0, revision: dataRef.current.revision + 1,
      })
      setLiveStatus("idle")
      setReadState({ key: requestKey, pending: false, error: null })
    }).catch((error: unknown) => {
      if (!cancelled) setReadState({ key: requestKey, pending: false, error: asError(error) })
    })
    return () => { cancelled = true }
  }, [acceptData, canLoad, key, query.keyword, query.limit, query.nodeId, query.severity, requestKey, source])

  const hasData = canLoad && data.key === key
  const pending = readState.key !== requestKey || readState.pending
  const readError = readState.key === requestKey ? readState.error : null

  useEffect(() => {
    if (!followTail || !hasData || pending || readError || query.nodeId === null) return
    const nodeId = query.nodeId
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    async function poll() {
      try {
        const response = await streamApplicationLogEntries({
          nodeId, source, limit: appLogPageLimit, cursor: dataRef.current.cursor,
          keyword: query.keyword, levels: levelsForSeverityFilter(query.severity),
        })
        const events = (await response.text()).split("\n").filter((line) => line.trim())
          .map((line) => JSON.parse(line) as ManagerApplicationLogStreamEvent)
        if (cancelled) return
        // Failed batches never advance the cursor, so a retry can read them again.
        if (events.some((event) => event.type === "error")) throw new Error("Live log read failed")
        const current = dataRef.current
        const incoming = events.flatMap((event) => event.type === "line" && event.item ? [event.item] : [])
        if (holdingPosition.current && current.entries.length + incoming.length > appLogMaxTailLimit) {
          setLiveStatus("bufferFull")
          setFollowTail(false)
          return
        }
        const cursor = events.reduce((last, event) => event.cursor || last, current.cursor)
        acceptData({
          ...current, cursor,
          entries: incoming.length ? [...current.entries, ...incoming].slice(-appLogMaxTailLimit) : current.entries,
          rotated: current.rotated || events.some((event) => event.type === "rotation"),
          received: current.received + incoming.length,
        })
        setLiveStatus(incoming.length ? "live" : "heartbeat")
      } catch {
        if (!cancelled) setLiveStatus("error")
      }
      if (!cancelled) timer = setTimeout(poll, livePollIntervalMs)
    }
    void poll()
    return () => {
      cancelled = true
      if (timer !== undefined) clearTimeout(timer)
    }
  }, [acceptData, followTail, hasData, key, pending, query.keyword, query.nodeId, query.severity, readError, source, data.revision])

  function changeQuery(patch: Partial<Query>) {
    setLiveStatus("idle")
    setQuery((current) => ({ ...current, ...patch, limit: appLogPageLimit }))
  }

  function retry() {
    if (nodeError) {
      setNodeError(null)
      setNodeRetry((value) => value + 1)
    } else if (sourceState.error || !activeSource) {
      setSourceState({ nodeId: null, items: [], error: null })
      setSourceRetry((value) => value + 1)
    } else {
      setRefresh((value) => value + 1)
    }
  }

  return {
    query, nodes, sources, source, activeSource, key, canLoad, hasData,
    entries: hasData ? data.entries : [],
    received: hasData ? data.received : 0,
    revision: data.revision,
    rotated: hasData && data.rotated,
    loading: (!nodes && !nodeError) || (query.nodeId !== null && !sourcesReady)
      || (canLoad && !hasData && !readError),
    refreshing: hasData && pending,
    error: nodeError ?? (sourcesReady ? sourceState.error : null) ?? readError,
    followTail, liveStatus,
    holdPosition: useCallback((holding: boolean) => { holdingPosition.current = holding }, []),
    changeNode: (nodeId: number | null) => changeQuery({ nodeId, source: "app" }),
    changeSource: (next: string) => changeQuery({ source: next }),
    changeSeverity: (severity: AppLogSeverityFilter) => changeQuery({ severity }),
    search: (keyword: string) => {
      changeQuery({ keyword: keyword.trim() })
      setRefresh((value) => value + 1)
    },
    clearFilters: () => changeQuery({ keyword: "", severity: "" }),
    refresh: () => setRefresh((value) => value + 1),
    loadMore: () => setQuery((current) => ({ ...current, limit: Math.min(appLogMaxTailLimit, current.limit + appLogPageLimit) })),
    setFollowTail: (follow: boolean) => {
      setLiveStatus("idle")
      setFollowTail(follow)
    },
    retry,
  }
}
