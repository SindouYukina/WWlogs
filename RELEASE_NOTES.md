# WWlogs v0.2.0 發布紀錄

日期：2026-11-01（Asia/Tokyo）
狀態：v0.2.0 正式版本；Windows 執行檔作為對應 GitHub Release 資產，不納入 Git tree。

## 內容

- Windows x64 Go 執行檔：GitHub Release 資產 `WWlogs.exe`，15,500,800 bytes，SHA-256 `4b9a76f51138cf13af084acc4b32ef9a1425557eddb82b49cf42c12fbe3420ee`。
- Master 資料庫：`data/master_data.db`，72,069,120 bytes，SHA-256 `9f2d56ba0e2577a000fd112ad4a1feba62b2ca6d56f988979d7862ffb682535f`；唯讀檢查為 298 張表、`integrity_check=ok`、`foreign_key_check` 無錯誤。本次未修改或刪除資料。
- 執行檔支援 `-version`、繁體中文 `-help`、啟動時列出本機 IPv4 Npcap 介面並由使用者選擇，以及依執行檔位置解析預設 Master 路徑。
- 私鑰不納入執行檔發行內容；建置時會將 AES-GCM 密文嵌入 EXE，執行時僅在記憶體解密。此設計避免明文直接寫入 EXE，仍不代表無法透過逆向還原。
- Master 更新的來源版本與產品資料庫來源版本不同。來源及用途限制不屬於此產品發布內容；未能證明非 resolver 表格可安全移除，因此保留完整資料庫。

## 驗證

- `scripts/test.ps1 -count=1`：PASS。
- `go test ./tests/versioninfo -count=1`：PASS。
- `go vet`（release-candidate tree，含 capture 與 Master builder）：PASS。
- `go build -trimpath -o bin/WWlogs.exe ./cmd/capture`：PASS。
- 執行檔 `-version`、`-help`：PASS；從 `bin/` 工作目錄使用預設路徑重播 golden PCAP：PASS。
- 網卡選擇輸入越界的安全拒絕檢查：PASS；此檢查未開始擷取。
- 從檔案總管雙擊執行：NOT RUN；本次未取得可操作的檔案總管視窗。
- 真實 Live/Npcap 擷取：NOT RUN；本次未選擇介面，也未開始即時擷取。

此紀錄不代表完整 live acceptance，也不聲稱 Master ID 已完成來源身分歸屬。私有 PCAP、執行記錄、金鑰和研究材料均留在本機忽略目錄。

## v0.1.2

- 保留 v0.1.1 的 KCP stream framing 與 compressed CRC 修正。
- 建置時從 Git 忽略的專案根目錄 `privateKey.pem` 載入並驗證 RSA 私鑰，以每次建置產生的 AES-GCM 密文嵌入執行檔。執行時只在記憶體中解密，不再接受 `-key-file` 參數。
- 建置器會掃描輸出執行檔，拒絕包含該私鑰 PEM 或 DER 明文的產物。
- 這項處理避免直接把 PEM／DER 明文放進 EXE；執行檔仍可經逆向還原，不應視為防止金鑰提取的保證。
- Live/Npcap：NOT RUN；本次未執行真實遊戲擷取驗收。
