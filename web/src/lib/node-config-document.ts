import type { IntlShape } from "react-intl"

import type { ManagerNodeConfigDocument } from "./manager-api.types"
import { nodeConfigItemLabel, nodeConfigSourceLabel } from "./node-config-i18n"

export type ConfigDocumentLine = {
  text: string
  baseLine: number
  annotation: boolean
  searchText: string
}

// Values are already TOML-encoded by the node. Only comments are added here.
export function configDocumentLines(document: ManagerNodeConfigDocument, intl: IntlShape, showHelp: boolean): ConfigDocumentLine[] {
  const fields = new Map(document.fields.map((field) => [field.line, field]))
  const lines = document.toml.replace(/\n$/, "").split("\n")
  return lines.flatMap((text, index) => {
    const baseLine = index + 1
    const field = fields.get(baseLine)
    const label = field ? nodeConfigItemLabel(intl, { key: field.env_key, label: field.label }) : ""
    const description = field
      ? (intl.locale.startsWith("zh") ? field.description_zh : field.description)
      : ""
    const searchText = [text, field?.path, field?.env_key, label, description].join(" ").toLowerCase()
    const result: ConfigDocumentLine[] = []
    if (showHelp && field) {
      const help = [label, description, intl.formatMessage({ id: "nodeConfig.toml.source" }, {
        source: nodeConfigSourceLabel(intl, field.source),
      })]
      for (const comment of help.flatMap((part) => part.split(/\r\n|\r|\n/))) {
        result.push({ text: "# " + comment, baseLine, annotation: true, searchText: comment.toLowerCase() })
      }
    }
    if (field?.redacted) {
      text = "# " + field.path.split(".").at(-1) + ": " + intl.formatMessage({ id: "nodeConfig.toml.hidden" })
    }
    result.push({ text, baseLine, annotation: false, searchText })
    return result
  })
}

export function configDocumentText(lines: ConfigDocumentLine[]) {
  return lines.map((line) => line.text).join("\n") + "\n"
}
