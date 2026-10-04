import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerNode, ManagerNodeConfigDocument, ManagerNodesResponse } from "@/lib/manager-api.types"
import { NodeConfigPage } from "./page"

const getNodesMock = vi.fn()
const getDocumentMock = vi.fn()
const getLegacyConfigMock = vi.fn()
const scrollIntoViewMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/lib/manager-api")>(),
  getNodes: (...args: unknown[]) => getNodesMock(...args),
  getNodeConfigDocument: (...args: unknown[]) => getDocumentMock(...args),
  getNodeConfig: (...args: unknown[]) => getLegacyConfigMock(...args),
}))

const nodesResponse: ManagerNodesResponse = {
  generated_at: "2026-09-08T10:00:00Z", controller_leader_id: 1, total: 2,
  items: [nodeFixture(2, "node-2", false), nodeFixture(1, "node-1", true)],
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getNodesMock.mockReset().mockResolvedValue(nodesResponse)
  getDocumentMock.mockReset().mockImplementation((nodeId: number) => Promise.resolve(configFixture(nodeId)))
  getLegacyConfigMock.mockReset()
  scrollIntoViewMock.mockReset()
  HTMLElement.prototype.scrollIntoView = scrollIntoViewMock
})

test("loads only the selected local node's TOML with descriptions off", async () => {
  renderPage()
  const document = await screen.findByLabelText("Startup TOML configuration")
  expect(document).toHaveTextContent("[cluster]")
  expect(document).toHaveTextContent("hash_slot_count = 256")
  expect(document).not.toHaveTextContent("Stable physical partitions")
  expect(screen.getByRole("checkbox", { name: "Show descriptions" })).not.toBeChecked()
  expect(screen.getByText("Effective startup configuration")).toBeInTheDocument()
  expect(getDocumentMock).toHaveBeenCalledTimes(1)
  expect(getDocumentMock).toHaveBeenCalledWith(1)
  expect(getLegacyConfigMock).not.toHaveBeenCalled()
})

test("shows localized detailed descriptions and source comments on demand", async () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")
  const user = userEvent.setup()
  renderPage()
  const document = await screen.findByLabelText("启动 TOML 配置")
  expect(document).toHaveTextContent("内容已隐藏")
  expect(document).not.toHaveTextContent("默认 256")
  await user.click(screen.getByRole("checkbox", { name: "显示说明" }))
  expect(document).toHaveTextContent("物理分区，默认 256，初始化后不能修改。")
  expect(document).toHaveTextContent("哈希槽位数量")
  expect(document).toHaveTextContent("来源：")
  expect(document).not.toHaveTextContent("Stable physical partitions")
  await user.click(screen.getByRole("checkbox", { name: "显示说明" }))
  expect(document).not.toHaveTextContent("默认 256")
  expect(document).toHaveTextContent("内容已隐藏")
})

test("search highlights and navigates the whole document without hiding fields", async () => {
  const user = userEvent.setup()
  renderPage()
  const document = await screen.findByLabelText("Startup TOML configuration")
  await user.type(screen.getByLabelText("Search config"), "jwt")
  expect(document).toHaveTextContent("hash_slot_count = 256")
  expect(document.querySelectorAll("mark")).toHaveLength(2)
  expect(document.querySelector('[data-active="true"]')).toHaveAttribute("data-line", "8")
  await user.click(screen.getByRole("button", { name: "Next match" }))
  expect(document.querySelector('[data-active="true"]')).toHaveAttribute("data-line", "9")
  await user.click(screen.getByRole("button", { name: "Previous match" }))
  expect(document.querySelector('[data-active="true"]')).toHaveAttribute("data-line", "8")
  await user.click(within(screen.getByRole("navigation", { name: "Configuration groups" })).getByRole("button", { name: "Cluster" }))
  expect(scrollIntoViewMock.mock.instances.at(-1)).toHaveAttribute("data-line", "4")
  await user.clear(screen.getByLabelText("Search config"))
  await user.type(screen.getByLabelText("Search config"), "no-such-setting")
  expect(screen.getByText("0/0 lines")).toBeInTheDocument()
  expect(document).toHaveTextContent("hash_slot_count = 256")
})

test("copies full TOML and optional descriptions even while searching", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } })
  renderPage()
  await screen.findByLabelText("Startup TOML configuration")
  await user.type(screen.getByLabelText("Search config"), "jwt")
  await user.click(screen.getByRole("button", { name: "Copy full TOML" }))
  await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
  expect(writeText.mock.calls[0][0]).toContain("[cluster]\nhash_slot_count = 256")
  expect(writeText.mock.calls[0][0]).toContain("# jwt_secret:")
  expect(writeText.mock.calls[0][0]).not.toContain("Stable physical partitions")
  await user.click(screen.getByRole("checkbox", { name: "Show descriptions" }))
  await user.click(screen.getByRole("button", { name: "Copy full TOML" }))
  const copied = writeText.mock.calls[1][0] as string
  expect(copied).toContain("# Stable physical partitions. Default 256; do not change after initialization.")
  expect(copied).toContain("# Source:")
  expect(copied).toContain("hash_slot_count = 256")
  expect(copied).not.toContain("SECRET_CANARY")
  expect(await screen.findByText("Copied")).toBeInTheDocument()
})

test("reports clipboard failure without reporting success", async () => {
  const user = userEvent.setup()
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined })
  renderPage()
  await screen.findByLabelText("Startup TOML configuration")
  await user.click(screen.getByRole("button", { name: "Copy full TOML" }))
  expect(await screen.findByText(/Copy failed/)).toBeInTheDocument()
  expect(screen.queryByText("Copied")).not.toBeInTheDocument()
})

test("honors a deep link and falls back when the target no longer exists", async () => {
  const first = renderPage("/cluster/node-config?node_id=2")
  await screen.findByLabelText("Startup TOML configuration")
  expect(getDocumentMock).toHaveBeenCalledWith(2)
  expect(screen.getByRole("button", { name: /node-2/i })).toHaveAttribute("aria-current", "true")
  first.unmount()
  getDocumentMock.mockClear()
  renderPage("/cluster/node-config?node_id=99")
  await screen.findByLabelText("Startup TOML configuration")
  expect(getDocumentMock).toHaveBeenCalledWith(1)
})

test("ignores a late response after switching nodes", async () => {
  const user = userEvent.setup()
  let resolveFirst!: (document: ManagerNodeConfigDocument) => void
  getDocumentMock.mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve }))
  renderPage()
  await waitFor(() => expect(getDocumentMock).toHaveBeenCalledWith(1))
  await user.click(screen.getByRole("button", { name: /node-2/i }))
  const document = await screen.findByLabelText("Startup TOML configuration")
  expect(document).toHaveTextContent("id = 2")
  await act(async () => resolveFirst(configFixture(1)))
  expect(document).toHaveTextContent("id = 2")
  expect(document).not.toHaveTextContent("id = 1")
})

test("shows an explicit unsupported state for old nodes and keeps the node rail", async () => {
  getDocumentMock.mockRejectedValueOnce(new ManagerApiError(501, "node_config_toml_unsupported", "unsupported"))
  renderPage()
  expect(await screen.findByText(/This node version does not support TOML/)).toBeInTheDocument()
  expect(screen.getByTestId("node-config-node-rail")).toHaveTextContent("node-2")
  expect(getLegacyConfigMock).not.toHaveBeenCalled()
})

test("clears the document when node inventory refresh fails", async () => {
  const user = userEvent.setup()
  renderPage()
  await screen.findByLabelText("Startup TOML configuration")
  getNodesMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  await user.click(screen.getByRole("button", { name: "Refresh" }))
  await screen.findByText(/currently unavailable/i)
  expect(screen.queryByLabelText("Startup TOML configuration")).not.toBeInTheDocument()
  expect(getDocumentMock).toHaveBeenCalledTimes(1)
})

function renderPage(path = "/cluster/node-config") {
  return render(<I18nProvider><MemoryRouter initialEntries={[path]}><NodeConfigPage /></MemoryRouter></I18nProvider>)
}

function configFixture(nodeId: number): ManagerNodeConfigDocument {
  return {
    generated_at: "2026-09-08T10:01:00Z", node_id: nodeId,
    source: "effective_startup_config", requires_restart: true,
    toml: `[node]\nid = ${nodeId}\n\n[cluster]\nhash_slot_count = 256\n\n[manager]\n# jwt_secret: hidden\njwt_issuer = "wukongim-manager"\n`,
    sections: [{ path: "node", line: 1 }, { path: "cluster", line: 4 }, { path: "manager", line: 7 }],
    fields: [
      { path: "node.id", env_key: "WK_NODE_ID", label: "Node ID", description: "Stable node identity.",
        description_zh: "稳定节点标识。", source: "toml", line: 2, redacted: false },
      { path: "cluster.hash_slot_count", env_key: "WK_CLUSTER_HASH_SLOT_COUNT", label: "Hash slot count",
        description: "Stable physical partitions. Default 256; do not change after initialization.",
        description_zh: "物理分区，默认 256，初始化后不能修改。", source: "default", line: 5, redacted: false },
      { path: "manager.jwt_secret", env_key: "WK_MANAGER_JWT_SECRET", label: "Manager JWT secret",
        description: "Signing secret.", description_zh: "签名密钥。", source: "env", line: 8, redacted: true },
      { path: "manager.jwt_issuer", env_key: "WK_MANAGER_JWT_ISSUER", label: "Manager JWT issuer",
        description: "JWT issuer.", description_zh: "JWT 签发方。", source: "toml", line: 9, redacted: false },
    ],
  }
}

function nodeFixture(nodeId: number, name: string, isLocal: boolean): ManagerNode {
  return {
    node_id: nodeId,
    name,
    addr: `10.0.0.${nodeId}:11110`,
    status: "alive",
    last_heartbeat_at: "2026-07-08T10:00:00Z",
    is_local: isLocal,
    capacity_weight: 1,
    membership: { role: nodeId === 1 ? "data" : "replica", join_state: "active", schedulable: true },
    health: { status: "alive", last_heartbeat_at: "2026-07-08T10:00:00Z" },
    controller: { role: nodeId === 1 ? "leader" : "follower", voter: true, leader_id: 1 },
    slot_stats: { count: nodeId === 1 ? 86 : 85, leader_count: nodeId === 1 ? 44 : 42 },
    slots: {
      replica_count: 86,
      leader_count: nodeId === 1 ? 44 : 42,
      follower_count: nodeId === 1 ? 42 : 43,
      quorum_lost_count: 0,
      unreported_count: 0,
    },
  }
}
