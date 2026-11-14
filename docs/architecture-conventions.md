# 架構慣例

maxos 的分層、依賴規則與技術選型。所有開發（含 subagent 派工）以本檔為準。

## 1. 系統定位

- 單一使用者的個人系統，跑在一台 Mac mini 上。
- 一個 Go binary（modular monolith）加一個 PostgreSQL。
- Context 依序加入：finance、health、project。Context 之間會互相關聯。
- 對外只有 MCP 端點，經 Cloudflare Tunnel 與 Cloudflare Access；其餘只在本機或 Tailscale 內。

## 2. 目錄結構

六角架構，參考 `likochat/admin-service`：`app/<context>/{domain,usecase,adapter}` + `pkg/` + `cmd/`。

```
main.go                          只呼叫 cmd.Execute()
cmd/                             Cobra 子指令與 FX 組裝
  root.go  serve.go  mcp_stdio.go  migrate.go
  dp.go                          所有 FX module 的組裝
app/
  <context>/
    domain/
      model/                     entity、value object、domain 錯誤
      service/                   跨 entity 的 domain 規則（需要時才建）
    usecase/
      <name>/                    use case 實作；new.go 提供 Module()
      port/
        in/usecase/              use case 介面與 Input / Output
        out/<kind>/              對外介面：repo、pricefeed 等
    adapter/
      in/<transport>/            mcp、cli、http
      out/
        persistence/postgres/    repo 實作、sqlc 查詢與產生碼
        <kind>/                  其他對外實作
pkg/                             與業務無關的技術元件
  config/  log/  postgres/  cfaccess/  mcpserver/
migrations/                      goose 的 SQL migration（Go package，內嵌進 binary）
internal/testpg/                 整合測試用的資料庫輔助
docs/
```

### Context 清單

| Context | 職責 | 狀態 |
|---|---|---|
| `inbox` | 所有外部輸入的原始紀錄與冪等 | M0 |
| `catalog` | project、area、party、tag、link（跨 context 關聯） | M0 建表，M1 起使用 |
| `finance` | 帳本、匯入、報表、投資 | M1 起 |
| `health` | 待定 | M6 之後 |
| `project` | 待定；可能是外部系統，以外部參照對接（欄位待定） | 待評估 |

## 3. 各層職責

| 層 | 放什麼 | 不能做什麼 |
|---|---|---|
| `domain/model` | 資料結構、不變條件、狀態轉換 | 不能 import 標準函式庫、`uuid`、decimal 以外的套件 |
| `domain/service` | 需要多個 entity 才能判斷的規則 | 同上 |
| `usecase/<name>` | 流程編排、交易邊界、呼叫 port | 不能 import adapter、`pgx`、MCP SDK |
| `usecase/port` | 介面與輸入輸出型別 | 不能有實作 |
| `adapter/in` | 把外部請求轉成 use case 的 Input | 不能含業務規則；只做格式轉換與錯誤對應 |
| `adapter/out` | port 的實作 | 產生的 DB 型別不能傳出 adapter |
| `pkg` | 技術元件 | 不能 import `app/` |

業務規則的歸屬：單一 entity 能判斷的放 `domain/model`；要查資料或跨 entity 的放 use case 或 `domain/service`。例如「文字長度 1 到 4000」屬於 inbox 的 domain，「同一冪等鍵回傳既有紀錄」屬於 use case。

## 4. 依賴規則

依賴只能由外往內：

```
adapter  →  usecase  →  domain
   ↓
  pkg
```

1. `domain` 不依賴任何其他層。
2. `usecase` 只依賴自己 context 的 `domain` 和 `usecase/port`。
3. `adapter` 可以依賴自己 context 的 `usecase/port`、`domain/model` 和 `pkg`。
4. **跨 context 只能 import 對方的 `usecase/port/in`。** 不能 import 對方的 `domain`、`adapter` 或 use case 實作。這比 admin-service 嚴格，原因見第 9 節。
5. 跨 context 的資料關聯透過 `catalog` 的 link 表達，不在資料表之間加外鍵。
6. 只有 `cmd/` 知道具體實作，負責把 adapter 注入 port。

以上規則由 golangci-lint 的 import 限制在 `make check` 檢查。

## 5. 技術選型

| 項目 | 選擇 | 備註 |
|---|---|---|
| 語言 | Go 1.27 | |
| 資料庫 | PostgreSQL 18 | Docker Compose，只綁 `127.0.0.1:55432` |
| DB 存取 | `pgx/v5` + sqlc | 見第 6 節 |
| Migration | goose，純 SQL，內嵌進 binary | `maxos migrate` 執行 |
| 主鍵 | UUIDv7，由應用程式產生 | |
| 金額與數量 | Postgres `numeric` + Go decimal 型別 | 全程不經過浮點數 |
| CLI | Cobra | |
| 組裝 | Uber FX | 每個 use case 套件在 `new.go` 提供 `Module()` |
| 日誌 | 標準函式庫 `log/slog` | stdio 模式只能寫到 stderr |
| 設定 | 環境變數，`.env` 不進版控 | |
| HTTP | 標準函式庫 `net/http` | |
| MCP | 官方 Go SDK，Streamable HTTP（stateless）與 stdio | |
| 對外入口 | Cloudflare Tunnel，只對應 MCP | |
| 認證 | Cloudflare Access（Managed OAuth、passkey）；origin 驗證 `Cf-Access-Jwt-Assertion` | 沒有關閉驗證的開關 |

## 6. 資料庫慣例

- **Schema 變更**只能透過新增 migration 檔，不修改已合併的 migration。
- **Migration 位置**：放在專案根目錄的 `migrations/`（Go package，以 embed 內嵌進 binary，`maxos migrate` 與整合測試都用它）；sqlc 的 schema 也來自這裡。
- **資料表前綴**用 context 名稱：`inbox_`、`catalog_`、`fin_`。
- **sqlc**：
  - 查詢檔與產生碼都放在該 context 的 `adapter/out/persistence/postgres/` 底下。
  - 產生的型別只在 adapter 內使用，repo 負責轉成 `domain/model`。
  - 版本固定，透過 `make sqlc` 執行。
  - `make check` 會重新產生並要求無差異。
- **手寫 SQL 的例外**：有可選篩選條件的動態查詢，可以在同一個 adapter 裡手寫，集中在 `queries.go`。
- **交易邊界**由 use case 決定，repo 不自行開交易。

## 7. 測試慣例

| 對象 | 方式 |
|---|---|
| `domain` | 純單元測試，不需要資料庫 |
| `usecase` | 單元測試，port 用假實作 |
| `adapter/out/persistence` | `*_integration_test.go`，用真的 PostgreSQL |
| `adapter/in` | 透過真的協定客戶端呼叫（MCP 用記憶體內 transport） |

- 整合測試用 `internal/testpg`：每個測試程式建一次套完 migration 的 template database，每個測試從它複製出獨立的空資料庫。
- 測試要能抓到使用者看得到的錯誤；不測接線、不測產生碼、不測預設值。
- 帳本的不變條件（借貸平衡、冪等、對帳）必須有測試。

## 8. 寫入安全慣例

適用於所有經 MCP 進來的寫入。

1. 每次寫入都帶冪等鍵，重複呼叫回傳既有結果。
2. 原始輸入先進 `inbox`，記下來源與身分。
3. 由模型辨識出的資料先存成草稿，使用者確認後才生效。
4. 工具是業務動作，不提供任意寫入資料表的工具。
5. 驗證失敗時回傳可讓模型自行修正的錯誤訊息。

## 9. 與 admin-service 的差異

| 項目 | admin-service | maxos | 理由 |
|---|---|---|---|
| SQL | 手寫 `queries.go` | sqlc 為主 | 程式碼由 agent 撰寫，需要產生階段的 schema 檢查；帳本的表寬，手寫 `Scan` 錯位的後果嚴重 |
| 跨 context 依賴 | 允許 import 對方的 model 與 adapter | 只能 import 對方的 `usecase/port/in` | Context 之間的關聯是 maxos 的核心需求，依賴不設限會快速糾結 |
| 日誌 | Zap | `slog` | 功能夠用，少一個依賴 |
| 設定 | Viper + `config.yaml` | 環境變數 | 設定項少，跑在 Compose |
| Migration | 放在外部 repo | goose 內嵌 | 單一 repo，沒有共用 schema 的其他服務 |
| HTTP 路由 | chi | 標準函式庫 | 目前只有一個 MCP 端點 |

## 10. 新增 context 的步驟

1. 在 `app/<context>/` 建立 `domain`、`usecase`、`adapter` 三層。
2. 新增 migration，資料表用該 context 的前綴。
3. 在 `sqlc.yaml` 加一組設定，輸出到該 context 的 postgres adapter。
4. 在 use case 套件的 `new.go` 提供 `Module()`，並在 `cmd/dp.go` 掛上。
5. 需要對外的動作，在 `adapter/in/mcp` 註冊工具。
6. 更新第 2 節的 context 清單。
7. 在 `.golangci.yml` 加上該 context 的 depguard 規則（複製 `inbox-*` 各條，改成該 context 的名稱與路徑）。
