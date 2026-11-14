# M1 帳本設計

M1（issue #4）的資料模型與分批計畫。用語以 `CONTEXT.md` 為準，分層與依賴以 `docs/architecture-conventions.md` 為準。本檔只記錄 M1 的設計決定；實作完成後與程式碼不一致之處以程式碼為準，並回頭修正本檔。

## 1. 分批

| PR | 內容 |
|---|---|
| M1a | `catalog` context（領域、專案、對象、標籤）；`finance` 的參考資料（標的、機構、帳戶、類別）；帳本核心：支出、收入、轉帳、換匯，草稿與確認，餘額查詢 |
| M1b | 往來與分攤、回饋、手續費與折扣、對帳與「未記錄」 |
| M1c | 分期與利息、買賣（只記數量與金額，成本計算在 M5）、預算與分配 |

M1 不含 MCP 工具（M3）、匯入（M2）、報表（M4）。use case 以測試驗證。

專案的任務不在 M1 的範圍；專案只有財務需要的欄位。

## 2. Context 的歸屬

| 概念 | Context | 理由 |
|---|---|---|
| 領域、專案、對象、標籤 | `catalog` | 之後 health、project 也會用 |
| 標的、機構、帳戶、類別、交易、往來、預算 | `finance` | |

`finance` 只透過 `catalog` 的 `usecase/port/in` 查詢與驗證 catalog 的資料。

**跨 context 的參照**：`finance` 的資料表以一般的 `uuid` 欄位存放 catalog 的 id（`project_id`、`area_id`、`party_id`、`tag_id`），不加外鍵，寫入前由 use case 經 catalog 的 port 驗證存在。`catalog_link` 保留給種類不固定的多對多關聯。架構規範第 4 節第 5 點隨 M1a 修正為此說法。

## 3. 內部模型：複式記帳

對外用語不出現借貸。內部每筆交易由多個**分錄**（posting）組成。

### 分錄的對象（`book`）

| `book` | 意義 | 必填 |
|---|---|---|
| `account` | 一個帳戶 | `account_id` |
| `expense` | 支出 | `category_id` |
| `income` | 收入 | `category_id` |
| `conversion` | 換匯與買賣的中介，讓每個標的各自平衡 | 無 |
| `dealing`（M1b） | 某個對象的往來 | `party_id` |
| `reward`（M1b） | 回饋 | 無 |
| `opening` | 期初餘額 | 無 |

### 不變條件

1. **每筆交易、每個標的，分錄的 `quantity` 加總為 0。** 由 domain 檢查（`Transaction.Validate`，每個種類的建構函式都會呼叫）；資料庫在提交時以延遲的 constraint trigger 再檢查一次（第 4 節）。兩層都有測試。
2. `quantity` 的正負以「該對象的增加」為正：帳戶增加為正；支出發生為正；收入發生為負。
3. `expense`、`income`、`reward` 的分錄必須有 `base_amount`（本位幣 TWD 的金額，符號與 `quantity` 相同，小數位數不超過本位幣的 `decimals`）。標的為 TWD 時等於 `quantity`（呼叫端若給了，必須相等）；否則由呼叫端提供交易當天的金額，寫入後不再變動。M1a 只收金額，每個類別行各帶一個，不收匯率，換算由呼叫端負責。M1 不查詢匯率來源。
4. 只有 `status = confirmed` 的交易計入餘額與任何統計。草稿可以修改與刪除；已確認的交易不能修改內容，只能作廢（`void`）後重記。
5. 帳戶的餘額 = 該帳戶所有已確認分錄的 `quantity` 依標的加總。不另存餘額欄位。

### 各種類的分錄（M1a）

| 種類 | 例子 | 分錄 |
|---|---|---|
| 支出 | 午餐 120 TWD，現金 | `account(現金) −120 TWD`；`expense(飲食) +120 TWD` |
| 支出（外幣） | 100 USD，Ether.fi 卡，當天 3,200 TWD | `account(卡) −100 USD`；`expense(飲食) +100 USD, base 3200` |
| 支出（多類別） | 全聯 500：食材 300、日用品 200 | 一筆 `account −500`；兩筆 `expense` |
| 收入 | 薪水 | `account +N`；`income(薪水) −N, base −N` |
| 轉帳 | 銀行 → 信用卡繳款 | `account(銀行) −N`；`account(卡) +N`（同一標的） |
| 換匯 | 3,000 USD → 93,000 TWD | `account −3000 USD`；`conversion +3000 USD`；`conversion −93000 TWD`；`account +93000 TWD` |
| 期初 | 建立帳戶時的餘額 | `account +N`；`opening −N` |

轉帳的兩個帳戶標的相同；標的不同就是換匯。換匯的兩邊必須都是幣別（買賣在 M1c）。

## 4. 資料表

主鍵都是應用程式產生的 UUIDv7。金額與數量用 `numeric`，Go 端用 `github.com/shopspring/decimal`（domain 允許 import，需加入 depguard 的 allow list）。

### catalog（migration `0002_catalog.sql`）

- `catalog_area`：加 `archived_at timestamptz`。
- `catalog_project`：加 `starts_on date`、`ends_on date`、`active_from date`、`active_to date`、`default_area_id uuid`（外鍵到 `catalog_area`，同一個 context 內）、`archived_at timestamptz`。檢查：`ends_on >= starts_on`；`active_to >= active_from`；進行期間落在專案期間內（兩者都有值時）；期間與進行期間各自兩端要同時有值或同時為空。
- `catalog_party`、`catalog_tag`：加 `archived_at timestamptz`。

預算欄位不放在 catalog（預算屬於 finance，M1c）。

### finance 參考資料（migration `0003_fin_reference.sql`）

- `fin_instrument`：`id`、`code text UNIQUE`（`TWD`、`USD`、`BTC`、`2330.TW`…）、`name`、`kind`（`currency`／`crypto`／`stock`／`fund`）、`is_currency bool`、`decimals smallint`（顯示與輸入的小數位數，0–18）、`created_at`、`archived_at`（第 5 節的驗證要求標的未封存）。`is_currency` 與 `kind` 分開：USDT 的 `kind` 是 `crypto`，但 `is_currency` 為真。migration 內建 `TWD`（本位幣，id 固定，`decimals` 取 ISO 4217 的 2，不可封存）。
- `fin_institution`：`id`、`name UNIQUE`、`created_at`、`archived_at`。
- `fin_account`：`id`、`name UNIQUE`、`institution_id` 可為空（外鍵）、`kind`（`cash`／`bank`／`card`／`exchange`／`brokerage`／`wallet`／`loan`）、`created_at`、`archived_at`。帳戶不綁定標的。
- `fin_category`：`id`、`direction`（`expense`／`income`）、`parent_id` 可為空（外鍵，最多兩層，父子的 `direction` 相同）、`name`、`created_at`、`archived_at`；`UNIQUE (direction, parent_id, name)`（`parent_id` 為空時也要唯一，用 `NULLS NOT DISTINCT`）。「最多兩層」與「同方向」由一個複合外鍵 `fin_category_parent_fkey` 保證，它需要兩個產生欄位 `is_top`、`parent_must_be_top`（應用程式不寫入）。

### 帳本（migration `0004_fin_ledger.sql`）

- `fin_transaction`：`id`、`kind`（`expense`／`income`／`transfer`／`exchange`／`opening`；M1b、M1c 以新 migration 擴充）、`status`（`draft`／`confirmed`／`void`）、`occurred_on date`、`occurred_at timestamptz` 可為空、`description text`、`merchant text`、`project_id uuid`、`area_id uuid`、`inbox_item_id uuid`、`idempotency_key text UNIQUE`、`is_trial bool`、`created_at`、`confirmed_at`、`voided_at`。
  - `description`、`merchant` 為 `NOT NULL DEFAULT ''`（沒有就是空字串）；`idempotency_key` 為 `NOT NULL`；`is_trial` 為 `NOT NULL DEFAULT false`，M1a 的程式不讀也不寫它，用途留待之後決定。
  - CHECK：`kind`、`status` 的值；狀態與時間欄位相符（草稿沒有 `confirmed_at`、`voided_at`；已確認有 `confirmed_at`；作廢兩者都有，因為只有已確認的能作廢）。索引：`(occurred_on DESC, id DESC)`（列表由新到舊）、`project_id`、`area_id`。
- `fin_posting`：`id`、`transaction_id`（外鍵，`ON DELETE CASCADE`）、`seq smallint`、`book`、`account_id` 可為空（外鍵）、`category_id` 可為空（外鍵）、`instrument_id`（外鍵）、`quantity numeric NOT NULL`、`base_amount numeric`、`memo text`；`UNIQUE (transaction_id, seq)`；CHECK：`book` 與必填欄位的對應（第 3 節的表）。索引：`(account_id, instrument_id)`、`(category_id)`。
  - `memo` 為 `NOT NULL DEFAULT ''`。`book` 與欄位的對應拆成三個 CHECK：`account_id` 只有 `account` 有、`category_id` 只有 `expense`／`income` 有、`base_amount` 只有 `expense`／`income` 有（`conversion`、`opening` 三者皆空）；另有 `quantity <> 0`，以及 `base_amount` 與 `quantity` 同號。M1b 加入 `dealing`、`reward` 時改寫這幾個 CHECK。
- `fin_transaction_tag`：`transaction_id`（外鍵，`ON DELETE CASCADE`，捨棄草稿時標籤一起刪）、`tag_id uuid`；主鍵為兩者。
- **平衡檢查**（不變條件 1 的資料庫層）：函式 `fin_assert_transaction_balanced` 與兩個 `DEFERRABLE INITIALLY DEFERRED` 的 constraint trigger：`fin_transaction_balanced`（新增交易時）、`fin_posting_balanced`（分錄新增、修改、刪除時）。資料庫交易提交時，對每一筆受影響且仍存在的交易檢查「至少有一筆分錄，且每個標的的 `quantity` 加總為 0」，違反時以 SQLSTATE `23514`、constraint 名稱 `fin_transaction_balanced` 中止提交。捨棄草稿時分錄連帶刪除，交易已不存在，所以通過。因為交易列與分錄是多條語句，寫入必須在同一個資料庫交易內（第 5 節）。

## 5. Use case（M1a）

### catalog（`app/catalog`）

port/in：建立、改名、封存、列出、依 id 查詢 —— 領域、專案、對象、標籤各一組；專案另有設定期間、進行期間（傳空值即清除）、預設領域；`ResolveProjectDefaults(date)`：回傳進行期間涵蓋該日期的專案與其預設領域（最多一個；有多個時回傳錯誤由呼叫端處理）。已封存的專案不算；預設領域在設定之後被封存時，不回傳領域。另有 `VerifyReferences`：一次檢查一批專案、領域、對象、標籤的 id 是否存在且未封存，供其他 context 在寫入前驗證。

### finance 參考資料（`app/finance`）

port/in：標的、機構、帳戶、類別的建立、封存、列出、查詢。本位幣不可封存；封存頂層類別時其子類別一併封存。

### 帳本（`app/finance`）

- `RecordExpense`、`RecordIncome`、`RecordTransfer`、`RecordExchange`、`SetOpeningBalance`：輸入為業務用語（帳戶、金額、類別…），由 domain 產生分錄。支出與收入以 `Lines`（類別、金額、本位幣金額、備註）表示，單一類別就是一行，多類別分攤就是多行。全部帶冪等鍵（去掉前後空白後 8 到 128 個字元，與 inbox 相同）；輸出是 `RecordOutput{Transaction, Duplicate}`：鍵已用過時回傳當初記下的交易，`Duplicate` 為真，不再驗證請求，也不比對內容。預設建立為草稿；`Confirm: true` 時直接以已確認寫入（給匯入用）。
- 冪等在併發下的做法：鍵在資料庫有唯一約束；use case 先在資料庫交易內查鍵，沒有才驗證並以 `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING` 寫入。同時進來的相同鍵，第二個的 INSERT 會等第一個提交，然後什麼都不寫、回傳 `repo.ErrDuplicateKey`，use case 再讀回第一個寫的交易。
- `ConfirmTransactions(ids)`、`DiscardDraft(id)`、`VoidTransaction(id)`：以 `SELECT ... FOR UPDATE` 鎖住交易列（確認時依 id 排序，避免互相等待）後，由 domain 檢查狀態轉換（草稿→已確認、草稿→刪除、已確認→作廢，其餘回 `ErrNotDraft`／`ErrNotConfirmed`）。`ConfirmTransactions` 全有或全無：先檢查全部，任何一筆不存在或不是草稿就一筆都不確認，錯誤列出所有有問題的。
- `GetTransaction(id)`、`ListTransactions(filter)`（狀態、日期區間、帳戶、專案、領域）：以 `Limit`（預設 50，上限 200）與 `Offset` 分頁，發生日由新到舊、同一天後記的在前；`HasMore` 表示後面還有。
- `GetBalances(accountID?)`：各帳戶各標的的餘額，只含已確認；有已確認分錄的帳戶與標的各一列，依帳戶名稱與標的代碼排序，分錄加總為 0 時仍列出。

驗證：帳戶、類別、標的存在且未封存；類別的 `direction` 與種類相符；專案、領域、標籤經 catalog 的 port 驗證；支出與收入的金額為正；小數位數不超過標的的 `decimals`。

專案與領域的帶入（`CONTEXT.md`：專案有一個預設的領域，底下的交易自動帶入，個別交易可以改）：

- 未指定專案：若 `ResolveProjectDefaults(occurred_on)` 有結果，帶入專案與其預設領域；找到多個進行中的專案時（`AmbiguousProjectsError`）不帶入專案與預設領域，也不算錯。
- 指定專案、未指定領域：不查進行期間，領域帶入該專案的預設領域（經 catalog 的 `GetProject` 取得預設領域的 id，再以 `GetArea` 確認未封存）；專案沒有預設領域，或預設領域已封存時不帶入領域，也不算錯。
- 指定領域：永遠優先於專案的預設領域，不查預設。

固定扣款的例外在 M3 處理。

交易邊界由 use case 決定：每個 Record*、確認、捨棄、作廢（含驗證所需的讀取與冪等鍵查詢）在同一個資料庫交易內完成，repo 不自行開交易。需要一個 `pkg/postgres` 的交易輔助（透過 context 傳遞 `pgx.Tx`）；use case 經 `usecase/port/out/tx.Transactor`（只有 `WithinTx`）使用它，由 `cmd/` 注入 `*postgres.DB`。`TransactionRepo.Insert` 以多條語句寫入交易、分錄與標籤，必須在資料庫交易內呼叫，否則平衡檢查會在第一條語句提交時拒絕。

## 6. 測試

- domain：分錄產生與不變條件（每個種類、外幣、多類別、不平衡時拒絕）。
- use case：以假的 port 測驗證規則與冪等。
- postgres adapter：整合測試，含 CHECK 限制、平衡檢查（不平衡的交易在提交時被拒絕）、餘額查詢只計已確認、草稿確認後餘額改變、作廢後餘額還原、捨棄草稿連分錄一起刪、全有或全無、併發重複冪等鍵。冪等與全有或全無的測試在 repo 層（`WithinTx` 加 `TransactionRepo`）：adapter 依 depguard 不能 import use case。
