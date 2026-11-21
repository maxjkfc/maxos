# 唯讀網頁設計

`maxos serve` 在 `/app/` 底下提供一組唯讀的網頁，看帳本裡的資料與圖表。用語以 `CONTEXT.md` 為準，分層與依賴以 `docs/architecture-conventions.md`（尤其第 11 節）為準。本檔記錄這組網頁的決定；與程式碼不一致之處以程式碼為準，並回頭修正本檔。

## 1. 範圍

- 只讀。之後頁面要能做操作（確認、記帳……），所以結構上留好位置（第 5 節），但現在沒有任何寫入的路徑。
- 與 `/mcp` 同一個 Cloudflare Access：沒有未驗證的入口，沒有關掉驗證的開關。
- 聊天式的查詢（MCP 的查詢工具）留給 M3，網頁與它無關。
- 介面是繁體中文，詞彙照 `CONTEXT.md`（帳戶、餘額、交易、種類、草稿、類別、領域、專案、預算、分配、往來、對象、帳單、結帳日、繳款日、入帳日、未到期分期、未記錄……），不用它的 `_Avoid_` 欄列的詞：`glossary_test.go` 從 `CONTEXT.md` 讀出每個 `_Avoid_` 的詞（名字是別的詞條、或是種類的名字的除外），每個測試取得的每一頁都對照它，連 404 與 400 的頁面也是。

## 2. 路由

每條路由都在 Access verifier 後面；沒有 token 的請求得到和 `/mcp` 一樣的 `401 unauthorized`。

| 路徑 | 頁面 | 參數 | 呼叫的 use case（另外每頁讀一次需要的參考清單） |
|---|---|---|---|
| `/app/` | 總覽：各機構的餘額、未到期分期、往來、等著確認的草稿 | | `GetBalances`、`GetDealings`、`ListInstalmentPlans`、`GetDraftSummary` |
| `/app/transactions` | 一個月的交易，可篩帳戶、狀態、種類 | `month=YYYY-MM`、`account`（帳戶 id，或 `none`：沒有帳戶的交易）、`status`、`kind`、`page` | `ListTransactions`（一頁 100 筆） |
| `/app/transactions/{id}` | 一筆交易的所有部分、標籤、所屬的分期計畫 | | `GetTransaction`、`GetInstalmentPlan`（屬於計畫時） |
| `/app/drafts` | 草稿依月份再依帳戶分組，附筆數與金額 | | `GetDraftSummary` |
| `/app/cards` | 信用卡：進行中的一期與最近結帳的十二期 | | `ListCardStatements`（每張設了結帳日的卡一次） |
| `/app/cards/{account}` | 一張帳單：數字、交易（舊的在前）、依類別的消費 | `closing=YYYY-MM-DD`（帳單裡的任何一天，預設今天） | `GetCardStatement` |
| `/app/budgets` | 領域與專案的預算、已花、分配、未分配 | `month=YYYY-MM` | `GetAreaBudgets`、`GetProjectBudgets` |
| `/app/spending` | 這個月依類別的支出，最近十二個月的支出合計 | `month=YYYY-MM` | `GetSpending`、`GetSpendingTrend` |
| `/app/static/app.css` | 唯一的樣式表 | | |
| `/app` | 轉到 `/app/`（驗證之後才轉） | | |

- `month` 不給就是今天所在的月（時區是 `MAXOS_TIMEZONE`，今天只來自時鐘 port）。查詢字串用 `url.ParseQuery` 讀：壞的跳脫、分號，與一頁收的參數給了不只一個值，都是 400 頁面；不合法的參數（不是月份、年份在 1900 到 2999 之外、不是 id、`page` 不是 1 到 10000）也是 400 頁面，說明是哪個參數該是什麼，不重複收到的值；不存在的 id、不是帳單的帳戶是 404 頁面；其他方法是 405；use case 失敗是 500 頁面，不說原因（記在日誌）。
- 一頁的 use case 呼叫數固定：列出來的東西一次取得，參考清單（帳戶、機構、標的、類別、領域、專案、對象、標籤）各讀一次，含已封存的，因為舊交易指著它們。信用卡總覽每張設了結帳日的卡呼叫一次，那是卡的張數，不是帳本的筆數。`TestPagesIssueAFixedNumberOfCalls` 用一列與四百列的世界各讀每一頁，呼叫要完全一樣。
- 新增的讀取在 finance 的 `port/in`：`GetSpending`（頂層類別的合計）、`GetSpendingTrend`（「已花」與預算同一個定義，見 `m1-ledger.md` 第 5 節的「網頁用的讀取」）、`GetDraftSummary`、`GetCardStatement` 的 `Charges`、預算的每個分配的 `Over`，以及 `ListTransactions` 的 `Kind` 與 `WithoutAccount` 篩選。

## 3. 結構與安全

```
cmd/serve.go                      newHTTPHandler：安全標頭與 Access verifier 在最外面，裡面是 /mcp 與 /app/
cmd/web.go                        WebModule：各 context 的 registrar 收進 Fx 的 web.registrars；填 adapter 的 Ports 的 Fx struct 也在這裡
pkg/webserver                     Registry/Site：版面、導覽、錯誤頁、標頭、格式；不認識 context
pkg/svgchart                      圖表：資料進、SVG 出的純函式
app/finance/adapter/in/web        finance 的頁面：handler、view、templates/*.gohtml
internal/webtest                  測試用的 Access 替身（自簽金鑰）
```

- **守門**：`newHTTPHandler` 最外面是 `webserver.Secure(verifier.Middleware(mux))`，所以 mux 自己回的轉址與 404 也在驗證後面：沒有 token 的請求只得到 401，每個回應都帶標頭；`/mcp` 因此也帶這些標頭，行為其餘不變。`Registry.Pages(userOf, guard)` 的 guard 與 userOf 仍是必填，頁面自己也被它包住，所以頁面不靠被掛在哪裡；顯示的使用者是 token 的 email（服務 token 是 `service:<id>`）。只認 `Cf-Access-Jwt-Assertion` 標頭，不認 `CF_Authorization` cookie。沒有任何繞過驗證的開關；測試用 `internal/webtest` 的自簽金鑰，視覺檢查也是（第 7 節）。
- **標頭**（`webserver.Secure`，每個回應，包括 401）：`Content-Security-Policy: default-src 'self'; script-src 'none'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`、`X-Frame-Options: DENY`。沒有 inline script 也沒有 inline style（進度條與圖表是 SVG 元素，以 class 上色），沒有 JavaScript、CDN、字型或圖片的外部請求。
- **方法**：只有 GET 與 HEAD，其他一律 `405` 並帶 `Allow: GET, HEAD`。
- **圖表**是伺服器產生的 inline SVG（`pkg/svgchart`：橫條圖、直條圖、進度條），所有文字都經過跳脫，連結只收站內路徑，幾何用 decimal 算，沒有浮點數。每張圖旁邊都有一張同樣數字的表，表是依據，圖是它的樣子；SVG 的 `<title>`/`<desc>` 也寫了同樣的數字給讀屏軟體。
- **可及性與版面**：真的 `<table>`、`<th scope>`、`<caption>`，數字靠右並用等寬數字，負數以負號（U+2212）標示，顏色只是第二個線索；超支同時有文字與顏色；亮色與暗色跟著系統（`prefers-color-scheme`），桌面寬度圖與表並排。手機寬度（40rem 以下）每一列排成一張小卡片，每格前面寫欄名（`data-label`；欄標題留給讀屏軟體，標記上的 `role` 讓它仍是一張表）；欄位多的表在手機與桌面之間保持寬度，在框內橫向捲動。
- **金額**依標的的小數位數、千分位；**日期**是帳本裡的日曆日（不轉時區），**時刻**（建立、確認、作廢、發生時間）以 `MAXOS_TIMEZONE` 顯示。

## 4. 各頁怎麼排、怎麼算

網頁只排版，數字是 use case 的（架構慣例第 11 節）。有取捨的地方：

- **總覽**：餘額依機構分組，沒有機構的最後；一個標的一行，未到期分期是另一行（負的，和餘額同號）。所有標的都是零、也沒有未到期分期的帳戶收進摺疊的清單，已封存又沒有餘額的不列。未到期分期的表列的是分期的本金，所以是正數（欄名「已到期的本金」「未到期分期」），旁邊一行字說明它在餘額裡是負數、另外列出。往來為正是「對方欠我」，為負是「我欠對方」，用文字說出來，全部為零的對象收進摺疊清單。草稿的表是「如果這些草稿今天全部確認，帳戶的餘額今天會有的變動」，另有一行「自己帳戶之間的轉帳」的筆數。
- **交易**：一頁 100 筆，超過時有上一頁、下一頁；篩選與月份的連結都保留其他篩選；帳戶篩選除了帳戶還有「沒有帳戶的交易」（`account=none`）。金額欄是每個帳戶的增減（帳戶、未到期分期、往來的順序找第一種有的）；金額不是帳戶的增減時（未到期分期、往來）一律寫出它是什麼（「好銀行信用卡・未到期分期」「往來・Bob」），不只在有好幾筆金額時。草稿的列有虛線邊與底色，並有「草稿」標籤；已作廢的列劃線。
- **交易明細**：每個分錄是一列，項目名稱由種類與帳簿決定（支出裡的 `expense` 分錄是「主要金額」，其他種類的是「手續費」，分期的是「利息」，對帳的是「未記錄」；支出裡正的 `dealing` 是「分攤」，負的是「往來（我欠對方）」），折扣是它所屬分錄之後的一列，不影響支出。項目與簿、種類、狀態、HTTP 狀態都用 `domain/model` 的常數與 `net/http` 的常數，不寫字串。
- **草稿**：用 `GetDraftSummary`，數字都是它的，頁面照寫。一筆草稿是 `ConfirmTransactions` 一次確認的單位：分期計畫算一筆（它的起點，在起點那個月），各期不另外算。金額是「如果這些草稿今天全部確認，帳戶的餘額今天會有的變動」：日期在今天之後的草稿與還沒到期的分期各期要到那一天才會影響餘額，所以不在金額裡（筆數照算）。帳戶的列包含轉帳進出這個帳戶的部分，一筆草稿動到兩個帳戶（轉帳）時兩邊各算一筆；月與總數的流出和流入不含「自己帳戶之間的轉帳」，那是另一列，只有筆數。草稿屬於一個帳戶，是它有分錄到那個帳戶（任何簿，所以消費分期的起點算在那張卡），這和交易頁的帳戶篩選同一個定義；沒有任何帳戶分錄的（對象先付、記成往來的支出）自成「沒有帳戶的草稿」一列，連到 `account=none`。每一組連到該月該帳戶的草稿清單，那份清單列的就是它算的那些（狀態是草稿時，`ListTransactions` 不列分期計畫的各期）。
- **信用卡帳單**：總覽的範圍是「今天往前十三個月」到「今天往後 31 天」，從中取進行中的一期（含今天的那一期）與最近十二期已結帳的。一張帳單的「依類別的消費」是 `GetCardStatement` 的 `Charges`：這一期卡被扣的款，每筆交易的扣款依它各行的順序分給各行（支出的行依頂層類別，手續費與分期利息也是支出的行，所以在它們自己的類別，與支出頁同一個；我替別人付的行是「分攤」），不夠解釋的是「其他」（轉帳、往來、分期的本金）；每個標的一張圖與一張表，加起來等於那一期的「本期消費」。頁面只寫 use case 分好的各行，合計取自帳單的數字。
- **預算**：領域用 `GetAreaBudgets` 該月的狀態，專案用 `GetProjectBudgets`（整個專案一個總額，不分月，頁面有一行字說專案不隨月份改變）。超支與每一個分配的「超過分配」是 use case 的 `Over`（已花大於預算或分配，剛好花完不算），頁面不比較。沒有預算的領域只顯示已花。
- **支出**：「已花」與預算同一個定義（已確認、`expense` 帳簿、本位幣金額、到今天為止）。每個頂層類別的合計、它自己的金額與子類別是 `GetSpending` 的 `TopLevel`，頁面不加總。每月合計的窗口是「到今天所在的月為止的十二個月」，標題是「最近十二個月每月的支出」；選的月份不在其中時，窗口改成到選定的月份為止，標題改成「起月 到 迄月 每月的支出」，所以選定的月份一定是其中一根，並以不同顏色標出。

## 5. 之後要加寫入的動作

網頁已經是這樣放的：handler 回傳 `(Page, error)`，`webserver` 負責驗證、標頭、方法與錯誤頁，`webserver.UserFrom(ctx)` 給出動作的人。要加一個動作（例如確認草稿），不必動現有頁面：

1. 在 `webserver.Site` 加 `Post(pattern, handler)`，並讓 `Site.methods`（現在只放行 GET 與 HEAD 的那一個地方）對有 POST 路由的路徑放行 POST；handler 要能回 303 轉址（POST 之後 GET）。
2. 在同一個地方加一個 middleware，對所有 POST 檢查請求來自本站（`Sec-Fetch-Site: same-origin`，沒有就比對 `Origin` 與 `Host`）與表單帶的 token（綁使用者與時間的 HMAC）；一處檢查，不是每個 handler 各做各的。CSP 的 `form-action 'self'` 已經允許本站的表單，不需要 JavaScript：要二次確認就多一頁確認頁。
3. 動作呼叫既有的 use case（`ConfirmTransactions`、`DiscardDraft`、`VoidTransaction`……），經 `Ports` 注入；依架構慣例第 8 節帶冪等鍵（表單渲染時產生，放在隱藏欄位），來源記為網頁、身分用 `UserFrom`。
4. 測試：`TestOnlyGETIsServed` 要改成「只有登記了 POST 的路由接受 POST，其他仍是 405」，並加 CSRF 的拒絕案例；`cmd/` 的測試走一遍真的動作。

## 6. 刻意不顯示的

- **淨資產**：需要價格（M4、M5），所以沒有這個數字，頁面也沒有說明它在哪裡。**損益、成本、匯率變動**同理。
- 草稿的確認與捨棄、記帳、預算的設定：現在都是唯讀。
- 收件（inbox）的內容與交易的冪等鍵：不是看帳要看的東西。
- 標籤只顯示在交易明細，沒有以標籤篩選或統計：標籤「不參與任何計算，只用來篩選」，要篩選時再加。
- 分頁以外的搜尋、匯出與排序：還沒有需要。

## 7. 測試與視覺檢查

- `pkg/webserver`：版面、導覽、錯誤頁、標頭、方法與守門（沒有 guard 就建不出頁面、被拒絕的請求不會執行 handler）、格式。`pkg/svgchart`：縮放（零、一根、全相等、極大極小、沒資料、負值）、跳脫、連結過濾、結構合法。
- `app/finance/adapter/in/web`：透過真的 mux 與 Access verifier（`internal/webtest`）：每條路由沒有 token、錯誤的 token、過期、受眾不對、只有 cookie 都是 401 且 use case 一個都沒被呼叫；有 token 是 200；非 GET 是 405；不存在的 id 與帳戶是 404、壞參數是 400、use case 失敗是不洩漏原因的 500；每個回應（含錯誤）帶安全標頭且沒有外部資源。每一頁的內容、空狀態、金額的小數位數與負號、`<script>` 在備註與商家裡不被執行、一頁的呼叫數固定、「今天」取自時鐘（台北凌晨與 UTC 的日期不同時）。查詢字串讀不懂（壞的跳脫、分號）或收的參數給了兩次是 400 而且 use case 一個都沒被呼叫；400 的頁面不重複收到的值（十萬字元的值也是）。每個測試取得的每一頁都對照 `CONTEXT.md` 的 `_Avoid_` 詞（`glossary_test.go`，詞從檔案讀）。
- `cmd/`：真的模組與 PostgreSQL，記帳並確認一個小帳本後，用有效的 token 讀每一頁，檢查關鍵數字；一個不需要資料庫的測試（`http_handler_test.go`）走過 mux 自己回答的路徑（`//app`、`/app/./x`、`/`、`/APP/`、`/mcp/`、`/app%2fx` 等）：沒有 token 時每一個都只得到 401（不轉址、不 404）並帶安全標頭，有 token 時是 mux 的回答，也帶標頭。
- 視覺檢查（不進版控）：用拋棄式的測試把一個 `web_` 開頭的臨時資料庫以真正的 use case 填滿，用 `internal/webtest` 的自簽金鑰與 verifier 起真正的 handler，用 Chrome for Testing 以手機與桌面寬度拍每一頁；程式與資料庫用完即刪。真正的 Cloudflare Access 在這裡無法測試。
