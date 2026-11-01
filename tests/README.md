# 產品測試

所有 WWlogs 產品測試原始碼、測試輔助程式與 fixtures 都放在此 `tests/` 目錄；`cmd/` 與 `internal/` 不放測試程式碼。

- `capture/` 與 `keyembed/` 保存白箱測試。`go-overlay.json` 將這些實體位於 `tests/` 的檔案映射到受測產品套件，並從 `tests/` 套件掃描中隱藏原始路徑。
- `versioninfo/` 保存使用 exported API 的產品測試。
- `.vscode/settings.json` 與 `scripts/test.ps1` 使用同一 overlay，讓 gopls、Go 測試與編輯器解析一致。

請在 repository 根目錄執行產品測試：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1
```

一般 `go test` 參數會轉交給測試命令，例如：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1 -run TestCurrentMetadataIsComplete
```

測試入口會明確指定 `cmd`、`internal` 與所需 `tests` 套件，不遞迴掃描 repository 中的 `work/` 或其他無關目錄。
