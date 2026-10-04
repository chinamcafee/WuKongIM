import { Check, Copy, X } from "lucide-react"
import { useEffect, useState } from "react"
import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"
import { redactLogText } from "../log-format"

export function LogCopyButton({ value, label, compact = false }: { value: string; label: string; compact?: boolean }) {
  const intl = useIntl()
  const [status, setStatus] = useState<"copied" | "copyFailed" | null>(null)
  useEffect(() => {
    if (!status) return
    const timer = setTimeout(() => setStatus(null), 2500)
    return () => clearTimeout(timer)
  }, [status])

  async function copy() {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard unavailable")
      await navigator.clipboard.writeText(redactLogText(value))
      setStatus("copied")
    } catch {
      setStatus("copyFailed")
    }
  }

  return (
    <span className="relative inline-flex shrink-0 items-center gap-2">
      <Button aria-label={label} onClick={() => void copy()} size={compact ? "icon-sm" : "sm"} type="button" variant={compact ? "ghost" : "outline"} title={label}>
        {status === "copied" ? <Check className="text-emerald-500" /> : status === "copyFailed" ? <X /> : <Copy />}
        {compact ? null : label}
      </Button>
      {status ? (
        <span className={compact ? "absolute right-0 top-full z-10 w-max max-w-56 rounded border border-border bg-popover px-2 py-1 text-xs text-popover-foreground shadow-sm" : "text-xs text-muted-foreground"} role="status">
          {intl.formatMessage({ id: `appLogs.${status}` })}
        </span>
      ) : null}
    </span>
  )
}
