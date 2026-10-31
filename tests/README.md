# 產品測試

此目錄的測試透過公開 API 驗證 WWlogs 產品行為。白箱產品測試與其所測試的 `internal/` 套件放在一起，讓 Go 工具與編輯器能直接解析套件符號。

請在 repository 根目錄執行產品測試：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1
```

一般 `go test` 參數會轉交給測試命令，例如：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test.ps1 -run TestCurrentMetadataIsComplete
```

產品測試入口會明確指定產品套件，不會遞迴掃描 repository 中無關的目錄。
