# 架構慣例

maxos 的分層、依賴規則與技術選型。所有開發（含 subagent 派工）以本檔為準。

## 1. 系統定位

- 單一使用者的個人系統，跑在一台 Mac mini 上。
- 一個 Go binary（modular monolith）加一個 PostgreSQL。
- Context 依序加入：finance、health、project。Context 之間會互相關聯。
- 對外只有 MCP 端點與唯讀的網頁（`/app/`），經 Cloudflare Tunnel 與同一個 Cloudflare Access；其餘只在本機或 Tailscale 內。

## 2. 目錄結構

六角架構，參考 `likochat/admin-service`：`app/<context>/{domain,usecase,adapter}` + `pkg/` + `cmd/`。

```
main.go                          只呼叫 cmd.Execute()
cmd/                             Cobra 子指令與 FX 組裝
  root.go  serve.go  mcp_stdio.go  migrate.go
  dp.go                          所有 FX module 的組裝
  web.go                         網頁的 FX module（WebModule）
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
      in/<transport>/            mcp（工具）、web（頁面）
      out/
        persistence/postgres/    repo 實作、sqlc 查詢與產生碼
        <kind>/                  其他對外實作
pkg/                             與業務無關的技術元件
  config/  log/  postgres/  cfaccess/  mcpserver/  webserver/  svgchart/  clock/
migrations/                      goose 的 SQL migration（Go package，內嵌進 binary）
internal/testpg/                 整合測試用的資料庫輔助
internal/webtest/               網頁測試用的 Access 替身（自簽金鑰與信任它的 verifier）
docs/
```

### Context 清單

| Context | 職責 | 狀態 |
|---|---|---|
| `inbox` | 所有外部輸入的原始紀錄與冪等 | M0 |
| `catalog` | 領域、專案、對象、標籤；link（種類不固定的多對多關聯） | M0 建表，M1a 起有程式 |
| `finance` | 帳本、匯入、報表、投資 | M1 起（M1a 有標的、機構、帳戶、類別與帳本核心：支出、收入、轉帳、換匯、草稿與確認、餘額） |
| `health` | 待定 | M6 之後 |
| `project` | 待定；可能是外部系統，以外部參照對接（欄位待定） | 待評估 |

## 3. 各層職責

| 層 | 放什麼 | 不能做什麼 |
|---|---|---|
| `domain/model` | 資料結構、不變條件、狀態轉換 | 不能 import 標準函式庫、`uuid`、decimal 以外的套件 |
| `domain/service` | 需要多個 entity 才能判斷的規則 | 同上 |
| `usecase/<name>` | 流程編排、交易邊界、呼叫 port | 不能 import adapter、`pgx`、MCP SDK |
| `usecase/port` | 介面、輸入輸出型別與錯誤型別（錯誤型別可以帶自己的 `Error()` 方法） | 不能有其他實作 |
| `adapter/in` | 把外部請求轉成 use case 的 Input | 不能含業務規則；只做格式轉換與錯誤對應 |
| `adapter/out` | port 的實作 | 產生的 DB 型別不能傳出 adapter |
| `pkg` | 技術元件 | 不能 import `app/` |

業務規則的歸屬：單一 entity 能判斷的放 `domain/model`；要查資料或跨 entity 的放 use case 或 `domain/service`。例如「文字長度 1 到 4000」屬於 inbox 的 domain，「同一冪等鍵回傳既有紀錄」屬於 use case。輸入端 adapter 只做格式：網頁把金額寫成標的的小數位數與千分位、把代碼換成用語，不加總、不分攤、不判斷 use case 給的數字（見第 11 節）。

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
5. 跨 context 的資料關聯用一般的 `uuid` 欄位存放對方的 id，不加外鍵；寫入前由 use case 經對方的 `usecase/port/in` 驗證存在（例如 `finance` 的 `project_id`、`area_id`、`party_id`、`tag_id`）。種類不固定的多對多關聯才用 `catalog_link`。同一個 context 內的資料表可以加外鍵。
6. 只有 `cmd/` 知道具體實作，負責把 adapter 注入 port。
7. **時間**：use case 與 domain 不呼叫 `time.Now()`、`time.Since()`、`time.Until()`（三者都讀牆上的時鐘）。use case 要知道現在或今天，只經自己 context 的 `usecase/port/out/clock`（介面只有 `Now()`），由 `cmd/` 以 `pkg/clock` 實作並注入，時區來自 `MAXOS_TIMEZONE`（預設 `Asia/Taipei`）；測試給假的時鐘。資料庫記下的寫入時間（`confirmed_at` 等）不在此限，那是何時寫入，不是使用者的「今天」。

以上規則由 golangci-lint 在 `make check` 檢查：第 1 至 6 點是 import 限制（depguard），第 7 點是 `forbidigo`，禁止 `app/<context>/domain` 與 `app/<context>/usecase` 的非測試檔使用 `time.Now`、`time.Since`、`time.Until`（`pkg/clock` 與 `cmd/` 不在此限）。輸入端 adapter 的不同種類（`adapter/in/mcp`、`adapter/in/web`）之間也不互相 import，網頁 adapter 不碰資料庫與 MCP SDK，見第 11 節。

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
| 網頁 | `html/template` 伺服器端渲染；圖表是伺服器產生的 inline SVG（`pkg/svgchart`）；一份內嵌的樣式表 | 沒有 JavaScript、CDN 或任何外部請求；`Content-Security-Policy: default-src 'self'` |
| 對外入口 | Cloudflare Tunnel，對應 MCP（`/mcp`）與唯讀網頁（`/app/`） | |
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
- **交易邊界**由 use case 決定，repo 不自行開交易。唯一的例外是需要用同一份快照讀好幾條語句的唯讀 repo 方法，它可以用 `DB.WithinSnapshot` 開唯讀的 `REPEATABLE READ` 交易（目前是 finance 的 `TransactionRepo.List` 與 `TransactionRepo.Statement`）；寫入一律由 use case 決定。
  - `pkg/postgres` 的 `DB` 是 repo 執行 SQL 的入口（`dbgen.New(postgres.NewDB(pool))`）：語句會自動加入 context 帶著的交易，沒有交易就直接走連線池，所以 repo 不需要知道交易的存在。
  - `DB.WithinTx(ctx, fn)` 開啟交易，並把交易放進傳給 `fn` 的 context；`fn` 回傳 `nil` 才提交，回傳錯誤或 panic 就回滾；`fn` 內再呼叫 `WithinTx` 會用 savepoint，內層回傳錯誤時（含 context 在提交前已結束，以及 `fn` 忽略失敗的語句後回傳 `nil`），內層的寫入一律還原，外層交易仍可繼續使用。
  - use case 不能 import `pkg/`：需要交易的 use case 在自己 context 的 `usecase/port/out/` 宣告只有 `WithinTx(ctx context.Context, fn func(ctx context.Context) error) error` 的介面（finance 的是 `usecase/port/out/tx` 的 `Transactor`），由 `cmd/` 把 `*postgres.DB` 注入。
  - 一筆資料分多條語句寫入、且資料庫有延遲到提交才檢查的約束時（例如 finance 的分錄平衡檢查），該 repo 方法必須在資料庫交易內呼叫；repo 的 port 要寫明這點。

## 7. 測試慣例

| 對象 | 方式 |
|---|---|
| `domain` | 純單元測試，不需要資料庫 |
| `usecase` | 單元測試，port 用假實作 |
| `adapter/out/persistence` | `*_integration_test.go`，用真的 PostgreSQL |
| `adapter/in` | 透過真的協定客戶端呼叫（MCP 用記憶體內 transport）；網頁透過真的 mux 與 Access 中介層發請求（`internal/webtest` 的自簽金鑰），檢查回傳的 HTML |
| `cmd` | `cmd/*_integration_test.go`，用 `cmd/` 的 Fx 模組（`CatalogModule`、`FinanceModule`…）加真的 PostgreSQL，驗證跨 context、使用者看得到的行為，不是測接線；adapter 與 use case 互相不能 import，只有 `cmd/` 同時知道兩邊，所以這類測試放在這裡 |

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
| HTTP 路由 | chi | 標準函式庫 | 只有 `/mcp` 與 `/app/` 兩組路徑，頁面用 `ServeMux` 的方法與萬用字元樣式 |

## 10. 新增 context 的步驟

1. 在 `app/<context>/` 建立 `domain`、`usecase`、`adapter` 三層。
2. 新增 migration，資料表用該 context 的前綴。
3. 在 `sqlc.yaml` 加一組設定，輸出到該 context 的 postgres adapter。
4. 在 use case 套件的 `new.go` 提供 `Module()`，並在 `cmd/dp.go` 掛上。
5. 需要對外的動作，在 `adapter/in/mcp` 註冊工具；需要頁面，在 `adapter/in/web` 登記頁面（第 11 節）。
6. 更新第 2 節的 context 清單。
7. 在 `.golangci.yml` 加上該 context 的 depguard 規則（複製 `inbox-*` 各條，改成該 context 的名稱與路徑）。

## 11. 輸入端 adapter：MCP 與網頁

兩種輸入端 adapter 用同一個做法：共用的註冊表放在 `pkg/`，不認識任何 context；每個 context 在自己的 `adapter/in/<種類>` 登記自己的工具或頁面；`cmd/` 把各 context 的登記函式放進 Fx 的 value group，組成註冊表，掛在 `serve` 的 HTTP server 上。`serve` 的 handler 最外面是安全標頭與 Cloudflare Access 的 verifier，包住整個 mux：mux 自己回的轉址（`//app`、`/app/./x`）與 404（`/`、`/APP/`）也在它們後面，沒有 token 的請求只會得到 401，且每個回應（401 也是）都帶標頭。`/mcp` 的 `Cache-Control` 是 `no-store, no-transform`（`webserver.NoStore`：MCP SDK 會把它改成 `no-cache`）；`OPTIONS *` 也由 handler 回答，不是 `net/http` 先回 200（server 設了 `DisableGeneralOptionsHandler`，由 `cmd.NewHTTPServer` 建）；被拒絕的路徑與原因在日誌裡最多 200 個字元。

| | MCP | 網頁 |
|---|---|---|
| 共用註冊表 | `pkg/mcpserver` | `pkg/webserver`（版面、導覽、安全標頭、錯誤頁、金額與日期的格式）；圖表在 `pkg/svgchart` |
| context 的登記 | `app/<context>/adapter/in/mcp`：`NewRegistrar(use case…)` | `app/<context>/adapter/in/web`：頁面的 handler、view 與內嵌的 `templates/*.gohtml`；`NewRegistrar(Ports)`，`Ports` 是一個沒有 Fx 的普通 struct |
| Fx | `MCPModule`，value group `mcp.registrars` | `WebModule`（`cmd/web.go`），value group `web.registrars`；填 `Ports` 的 Fx struct 也在 `cmd/web.go`，adapter 不認識 Fx |
| 路徑 | `/mcp` | `/app/` 底下 |
| 守門 | `verifier.Middleware`（在 `newHTTPHandler` 最外面） | 同一個 verifier；它同時是 `webserver.Registry.Pages` 必填的參數，沒有 guard 就建不出頁面 |

網頁 adapter 可以 import 的，由 depguard 的 `finance-adapter-in-web`（一份 strict 的白名單，單一 package 的項目以 `$` 結尾，連名字只是長得像的 `pkg/webserverx` 也擋；測試檔另有 `finance-adapter-in-web-tests`，多一個 `internal/webtest` 與 `pkg/cfaccess`）檢查，只有這些：標準函式庫、`uuid` 與 decimal；自己 context 的 `usecase/port/in/`（use case 的 in-port）與 `usecase/port/out/clock`（時鐘；不是 repo，也不是 `tx`）；`domain/model`（它的名稱常數：種類、帳簿、狀態、帳戶種類，它的錯誤，與取月份和日期的純函式；頁面用常數，不寫字串）；別的 context 的 `usecase/port/in/`（例如 catalog 的名稱清單）；`pkg/webserver`、`pkg/svgchart`。其餘一律不行：use case 實作、`adapter/out`、別種輸入端 adapter（包括名字只是長得像的，如 `adapter/in/webhook`）、`pgx`、`pkg/postgres`、`pkg/mcpserver`、MCP SDK、Fx。`finance-adapter-in-others` 另外擋別種輸入端 adapter 反過來 import 網頁；之後有別種輸入端 adapter 時，各加一條對應的白名單。

網頁 adapter 另外遵守：

- **只讀。** 只服務 GET 與 HEAD，其他方法回 405。之後要加寫入的動作，在 `webserver` 加 `Post` 與一個檢查來源（`Origin`、`Sec-Fetch-Site`）與 token 的 middleware，頁面的 handler 不必動；做法寫在 `docs/design/web-readonly.md`。
- **一頁的 use case 呼叫數固定，不隨列數增加。** 列出來的東西（交易、草稿、帳單……）一次取得，名稱用的參考清單（帳戶、類別、領域……）每種一次；不在迴圈裡呼叫 use case。要的資料 use case 給不出來時，加 use case，不在 adapter 裡逐筆查。唯一隨數量增減的是信用卡帳單總覽：每張設了結帳日的卡一次 `ListCardStatements`（帳單的日子是各卡自己的），那是卡的張數，不是帳本的筆數。
- **頁面只排版：不加總，不分攤，不判斷。** 頁面做的是：金額的小數位數、千分位與正負號（一個 use case 給的數寫成「從合計扣掉」時前面的負號也是格式，如草稿頁「自己帳戶之間的轉帳」那一列）、日期、把代碼換成用語、列的順序（照 use case 給的；把它給的幾列併進同一個表時照金額排）、某個數字占它的總額的百分比、同一個回答裡兩個數字的差（預算與已花之間的「還剩」與「超支多少」）。這些不是頁面的：把幾筆加起來（一張帳單的消費依頂層類別、一個類別連同子類別的合計、草稿的流出與流入）、把一個數字分到各項、判斷一個狀態（超支、超過分配）、決定什麼算一筆草稿。缺的時候加 use case：這些現在分別在 `GetCardStatement` 的 `Charges`、`GetSpending` 的 `TopLevel`、預算的 `Over`（領域、專案與每一個分配）、`GetDraftSummary`（含 `OwnTransferAmounts`，讓表上每一欄加得起來）。
- **「今天」只來自時鐘 port**，時區是 `MAXOS_TIMEZONE`。交易的日期是帳本裡的日曆日，原樣顯示、不轉時區；建立、確認、作廢與發生的時刻（有時分的）才轉成這個時區顯示。
- **查詢參數要讀得出來，讀不出來是 400。** 查詢字串用 `url.ParseQuery` 讀，壞的跳脫與分號是 400，一頁收的參數給了不只一次也是 400（各頁共用一個 `queryOf`），不能像 `URL.Query()` 那樣悄悄丟掉讀不懂的，讓一個壞的月份看起來像本月。400 的頁面說參數該是什麼，不重複收到的值。
- **沒有外部資源。** 模板與唯一的一份樣式表以 `go:embed` 內嵌，圖表是伺服器產生的 inline SVG，沒有 JavaScript；每個回應帶 `Content-Security-Policy: default-src 'self'` 等標頭（`webserver.Secure`）。
- 畫面上的用語照 `CONTEXT.md`：測試從 `CONTEXT.md` 讀出每個 `_Avoid_` 的詞（名字是別的詞條、或是種類的名字的除外），每個測試取得的每一頁（`site.page`）都對照它，不另外維護一份詞表。

測試：用 `internal/webtest` 的 Access 替身（自簽金鑰、信任它的 verifier、簽好的 token）透過真的 mux 發請求，檢查拒絕、405、404 與 400 的頁面、標頭與 HTML；圖表是純函式，單獨測；`cmd/` 的測試用真的模組與 PostgreSQL 走一遍，另有一個不需要資料庫的測試走過 mux 自己回答的路徑，檢查沒有 token 時只會得到 401；`OPTIONS *` 走真的 socket、用與 `serve` 同一份 server 設定，`/mcp` 以真的 `capture` 呼叫驗證標頭與結果。
