# Go 測試

白箱測試集中放在 `tests/`，由 `scripts/test.ps1` 使用 Go overlay 注入對應的 `internal/` 套件。這保留對未匯出實作的測試能力，也讓測試檔案與產品程式碼分開管理。

請使用官方測試入口：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1
```

可將一般 `go test` 參數傳給 runner，例如：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1 -run TestExtractCheckClientPushKeepsOnlyThreeVersionStrings
```

capture 與 master 的白箱測試透過 overlay 提供給正式套件；versioninfo 測試只使用公開 API，採外部測試套件，可直接由 VS Code 與 `go test ./tests/versioninfo` 解析及執行。Overlay 檔只在執行期間建立於暫存目錄，結束後移除。`go test ./...` 不會自動把 `tests/` 中的白箱測試套用到其對應套件；應以官方 runner 的結果為準。
