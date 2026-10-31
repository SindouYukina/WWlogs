# WWlogs v0.2.1 發布候選

日期：2026-11-01（Asia/Tokyo）
狀態：本機 release candidate。GitHub repository 目前無法存取；尚未 push、尚未建立 GitHub Release，也尚未移除 v0.2.0 Release 或 tag。

Windows x64 產物：`WWlogs.exe`，15,515,648 bytes，SHA-256 `c6e34a937c23a32d9732694b3e551d5d07f5314374963c352d452e3d54459e23`。建置方式：`go run ./scripts/embed-key.go -o ./bin/WWlogs.exe`。本機發布副本位於忽略版控的 `work/github-release-assets/v0.2.1/WWlogs.exe`，與 `bin/WWlogs.exe` 的雜湊一致。

Master DB：`data/master_data.db`，72,069,120 bytes，SHA-256 `9f2d56ba0e2577a000fd112ad4a1feBA62B2CA6D56F988979D7862FFB682535F`。

## 修正

- 只在來源與目標都能由已驗證的 runtime identity 確認為非玩家怪物或模板實體，且沒有已知玩家歸屬線索時，才隱藏該戰鬥事件。
- 任一端能確認為本機角色或沿已驗證的 `SummonerComponent.SummonerId` 鏈歸屬本機角色時，保留事件。摩托車傷害、玩家相關 Buff、Boss 攻擊玩家及玩家攻擊怪物都不能被此 filter 排除。
- 關聯資料不足、身分未知或 owner chain 未閉合時，事件仍保留在一般輸出、Buff overlap 與 encounter 摘要；統計另列為 `included_unresolved_relation_events`。只有兩端均確認無關時才計入 `suppressed_confirmed_unrelated_events`。
- 未確認而保留的一般輸出會加上 `[PlayerRelation: UNKNOWN]` 標記；彙總統計分開列出已確認玩家關聯、未確認關聯與已確認無關事件。
- Buff snapshot 與 lifecycle 輸出不受 damage filter 控制；完整解析事件仍保留於 structured JSONL、audit 與研究診斷。
- 未增加 DPS、Buff tracker、Master Data 變更或 Live/Npcap 功能。

## 驗證

- `scripts/test.ps1 -count=1`：PASS。
- `go vet ./cmd/capture ./internal/capture`：PASS。
- Windows x64 build、`-version`、`-help`、PEM/DER 明文掃描：PASS；版本輸出為 `v0.2.1`。
- Locking PCAP：116 筆／584,134；其中 32 筆已確認玩家關聯、84 筆關聯未確認；0 筆被判定為已確認無關而隱藏。`-verify-baseline` PASS；Buff snapshots 297 筆；parse、decrypt、compression 與 role-report parse errors 均為 0。
- ECHO-CAST-01：21 筆／347,111；9 筆已確認玩家關聯、12 筆關聯未確認；0 筆確認無關；Buff snapshots 565 筆；解析錯誤 0。
- Florudlis hologram：87 筆／549,092；76 筆已確認玩家關聯、11 筆關聯未確認；0 筆確認無關；Buff snapshots 249 筆；解析錯誤 0。
- 單元回歸涵蓋怪物互打、怪物與 NPC、NPC 與怪物的確定無關過濾，以及玩家／摩托車／Boss 受擊／未知關聯／Buff snapshot 的保留。
- Live/Npcap：NOT RUN。

## 限制與發布狀態

三組 PCAP 都沒有足夠證據把任何事件確定分類為與玩家無關，因此這次 replay 沒有隱藏事件。這是保留未知資料的預期結果；單元測試另外驗證雙端已確認為環境實體時會被過濾。玩家關聯目前只由精確的本機 current-role entity 與已驗證的 Summoner owner chain 證明；其他關聯若無證據會保留並標示為 unresolved。

目前 Git remote 不存在，GitHub repository 尚未恢復，因此公開 push、GitHub Release 與 v0.2.0 遠端 Release/tag 清理尚未執行。v0.2.0 tag 暫時保留，直到 v0.2.1 成功發布並驗證。

# WWlogs v0.2.0 歷史發布紀錄

日期：2026-11-01（Asia/Tokyo）
狀態：曾發布的歷史版本；v0.2.1 尚未公開前，依替換順序保留本機 v0.2.0 Tag。GitHub repository 目前不存在，無法查驗或刪除遠端 Release／Tag。

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
