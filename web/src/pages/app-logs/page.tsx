import { useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { Button } from "@/components/ui/button"
import { ManagerApiError } from "@/lib/manager-api"
import { AppLogConsole } from "./components/app-log-console"
import { AppLogsToolbar } from "./components/app-logs-toolbar"
import { appLogMaxTailLimit, useAppLogs } from "./use-app-logs"

function mapErrorKind(error: Error) {
  if (error instanceof ManagerApiError) {
    if (error.status === 403) return "forbidden"
    if (error.status === 404) return "empty"
    if (error.status === 503) return "unavailable"
  }
  return "error"
}

export function AppLogsPanel() {
  const intl = useIntl()
  const logs = useAppLogs()
  const [keyword, setKeyword] = useState(logs.query.keyword)
  const title = intl.formatMessage({ id: "appLogs.title" })
  const lineCount = intl.formatMessage({ id: "appLogs.lineCount" }, { count: logs.entries.length })
  const hasFilters = Boolean(keyword || logs.query.keyword || logs.query.severity)
  const liveMessage = logs.liveStatus === "bufferFull"
    ? intl.formatMessage({ id: "appLogs.status.bufferFull" })
    : logs.followTail && logs.liveStatus !== "idle"
      ? intl.formatMessage({ id: `appLogs.status.${logs.liveStatus}` })
      : ""

  function clearFilters() {
    setKeyword("")
    logs.clearFilters()
  }

  return (
    <div className="space-y-3">
      <AppLogsToolbar
        activeSource={logs.activeSource}
        appliedKeyword={logs.query.keyword}
        canLoad={logs.canLoad}
        followTail={logs.followTail}
        hasFilters={hasFilters}
        keyword={keyword}
        lineCount={lineCount}
        liveMessage={liveMessage}
        loading={logs.loading}
        nodes={logs.nodes}
        onClearFilters={clearFilters}
        onFollowTailChange={logs.setFollowTail}
        onKeywordChange={setKeyword}
        onNodeChange={logs.changeNode}
        onRefresh={logs.refresh}
        onSearch={() => logs.search(keyword)}
        onSeverityChange={logs.changeSeverity}
        onSourceChange={logs.changeSource}
        refreshing={logs.refreshing}
        selectedNodeId={logs.query.nodeId}
        severity={logs.query.severity}
        source={logs.source}
        sources={logs.sources}
      />
      {logs.error && logs.hasData ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm" role="alert">
          <span>{intl.formatMessage({ id: "appLogs.refreshFailed" })}</span>
          <Button onClick={logs.retry} size="sm" variant="outline">{intl.formatMessage({ id: "common.retry" })}</Button>
        </div>
      ) : null}
      {logs.loading ? (
        <ResourceState kind="loading" title={title} />
      ) : logs.error && !logs.hasData ? (
        <ResourceState kind={mapErrorKind(logs.error)} onRetry={logs.retry} title={title} />
      ) : !logs.canLoad ? (
        <ResourceState description={intl.formatMessage({ id: "appLogs.noSources" })} kind="empty" onRetry={logs.query.nodeId === null ? undefined : logs.retry} title={title} />
      ) : (
        <AppLogConsole
          activeSource={logs.activeSource}
          entries={logs.entries}
          followTail={logs.followTail}
          keyword={logs.query.keyword}
          key={logs.key}
          lineCount={lineCount}
          onHoldingChange={logs.holdPosition}
          onLoadMore={logs.loadMore}
          received={logs.received}
          refreshing={logs.refreshing}
          revision={logs.revision}
          rotated={logs.rotated}
          source={logs.source}
          tailLimit={logs.query.limit}
          title={title}
          emptyDescription={intl.formatMessage({ id: logs.query.keyword || logs.query.severity ? "appLogs.empty.filtered" : "appLogs.empty" })}
          maxTailLimit={appLogMaxTailLimit}
        />
      )}
      <p className="text-xs leading-5 text-muted-foreground">
        <span>{intl.formatMessage({ id: "appLogs.scopeHint" })}</span>
        {" · "}{intl.formatMessage({ id: "appLogs.windowHint" })}
      </p>
    </div>
  )
}

export function AppLogsPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "appLogs.description" })}
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.systemLogs" })}
        title={intl.formatMessage({ id: "appLogs.title" })}
      />
      <AppLogsPanel />
    </PageContainer>
  )
}
