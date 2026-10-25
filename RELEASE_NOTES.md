# 發行說明

## v0.1.2

- 保留 v0.1.1 的 KCP stream framing 與 compressed CRC 修正。
- 建置時從 Git 忽略的專案根目錄 `privateKey.pem` 載入並驗證 RSA 私鑰，以每次建置產生的 AES-GCM 密文嵌入執行檔。執行時只在記憶體中解密，不再接受 `-key-file` 參數。
- 建置器會掃描輸出執行檔，拒絕包含該私鑰 PEM 或 DER 明文的產物。
- 這項處理避免直接把 PEM／DER 明文放進 EXE；執行檔仍可經逆向還原，不應視為防止金鑰提取的保證。
- Live/Npcap：NOT RUN；本次未執行真實遊戲擷取驗收。
