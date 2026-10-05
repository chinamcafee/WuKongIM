# 未部署 Fork 的 Cloud Lease 定时清理停用

依据：用户明确说明当前 Fork 未正式发布且无线上部署；运行 37250469013 的失败日志、cloud-lease-release.yml 与 GitHub Actions 工具目录。

范围：只禁用 chinamcafee/WuKongIM 的 cloud-lease-release.yml 在 GitHub Actions 上的执行状态；保留文件及未来 Acquire 自动重新启用机制，不改业务代码、回归 CI、云凭据或资源。

- [x] D01：确认失败步骤、工作流职责、云租约创建历史及停用适用性。
- [x] D02：在 GitHub 禁用指定工作流，实际验证 disabled_manually 状态，保存证据并提交记录。
