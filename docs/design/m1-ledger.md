# M1 帳本設計

M1（issue #4）的資料模型與分批計畫。用語以 `CONTEXT.md` 為準，分層與依賴以 `docs/architecture-conventions.md` 為準。本檔只記錄 M1 的設計決定；實作完成後與程式碼不一致之處以程式碼為準，並回頭修正本檔。

## 1. 分批

| PR | 內容 |
|---|---|
| M1a | `catalog` context（領域、專案、對象、標籤）；`finance` 的參考資料（標的、機構、帳戶、類別）；帳本核心：支出、收入、轉帳、換匯，草稿與確認，餘額查詢 |
| M1b | 往來與分攤、回饋、手續費與折扣、對帳與「未記錄」 |
| M1c | 分期與利息、買賣（只記數量與金額，成本計算在 M5）、預算與分配。分期、買賣與「今天」的判斷（日期在未來的交易到那天才計入）先做，本檔的 M1c 段落記的就是這些；預算與分配另做 |

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
| `dealing` | 某個對象的往來 | `party_id` |
| `reward` | 回饋 | 無 |
| `adjustment` | 非幣別標的對帳時的數量修正，與帳戶的分錄互相對沖 | 無 |
| `opening` | 期初餘額 | 無 |

### 不變條件

1. **每筆交易、每個標的，分錄的 `quantity` 加總為 0。** 由 domain 檢查（`Transaction.Validate`，每個種類的建構函式都會呼叫）；資料庫在提交時以延遲的 constraint trigger 再檢查一次（第 4 節）。兩層都有測試。
2. `quantity` 的正負以「該對象的增加」為正：帳戶增加為正；支出發生為正；收入發生為負；回饋取得為負（與收入相同）；往來為正表示對方欠我更多，為負表示對方欠我更少（或我欠對方）。
3. `expense`、`income`、`reward` 的分錄必須有 `base_amount`（本位幣 TWD 的金額，符號與 `quantity` 相同，小數位數不超過本位幣的 `decimals`）。標的為 TWD 時等於 `quantity`（呼叫端若給了，必須相等）；否則由呼叫端提供交易當天的金額，寫入後不再變動。M1a 只收金額，每個類別行各帶一個，不收匯率，換算由呼叫端負責。M1 不查詢匯率來源。往來（`dealing`）與 `adjustment` 沒有 `base_amount`：往來以雙方結算的幣別計，只在算淨資產時才換算。
4. 只有 `status = confirmed` 的交易計入餘額與任何統計。草稿不修改：要更正就捨棄後重記（M1a 沒有修改草稿的 use case）；已確認的交易不能修改內容，只能作廢（`void`）後重記。**唯一的例外是分期計畫的排程**：計畫的各期（扣款日、本金、利息、期數）可以整體換成新的（`RescheduleInstalment`，第 5 節），草稿與已確認的計畫都行，因為各期是機構何時、扣多少，那會變；起點與被分期的總額不在此限。
5. 帳戶的餘額 = 該帳戶所有已確認分錄的 `quantity` 依標的加總。不另存餘額欄位。
6. **折扣**只留作記錄：`discount` 只在 `expense` 分錄上、必須大於 0、小數位數不超過標的的 `decimals`。`quantity` 是實付金額，折扣不是分錄，也不進任何餘額或統計（`CONTEXT.md`：支出以實付金額計）。
7. **帳本不檢查餘額是否足夠。** 賣出比持有的多、從帳戶付款付到負數，都照收，餘額可以是負的；這是設計，不是漏洞：帳戶的餘額就是分錄的加總（第 5 點），不是上限（信用卡本來就是負的）。`Validate` 與資料庫都沒有這種檢查，要不要攔是呼叫端的事。

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

轉帳的兩個帳戶標的相同；標的不同就是換匯。換匯的兩邊必須都是幣別；一邊是非幣別標的的是買賣（M1c，見下）。

### 各種類的分錄（M1b）

| 種類 | 例子 | 分錄 |
|---|---|---|
| 分攤 | 刷卡付 900，自己的份 300，A、B 各欠 300 | `account(卡) −900`；`expense(類別) +300, base 300`；`dealing(A) +300`；`dealing(B) +300` |
| 分攤（外幣） | 刷卡付 100 USD，自己的份 40 USD（當天 1,280 TWD），A 欠 60 USD | `account(卡) −100 USD`；`expense(類別) +40 USD, base 1280`；`dealing(A) +60 USD`（往來沒有 base） |
| 別人替我付 | 朋友 F 付了我的 300 晚餐 | `dealing(F) −300`；`expense(類別) +300, base 300`（沒有帳戶分錄） |
| 往來增加 | 我替公司 C 付 5,000（公司代付），或借出 | `account −5000`；`dealing(C) +5000` |
| 往來減少 | C 還 5,000 | `account +5000`；`dealing(C) −5000` |
| 回饋 | 信用卡回饋 50 | `account +50`；`reward −50, base −50` |
| 手續費 | 從 A 轉 1,000 到 B，手續費 15 | `account(A) −1000`；`account(B) +1000`；`account(A) −15`；`expense(手續費類別) +15, base 15` |
| 折扣 | 店家折 50，實付 450 | `account −450`；`expense +450, base 450, discount 50` |
| 對帳（幣別，比記錄少） | 帳戶實際比記錄少 120 | `account −120`；`expense(未記錄) +120, base 120` |
| 對帳（幣別，比記錄多） | 帳戶實際比記錄多 80 | `account +80`；`income(未記錄) −80, base −80` |
| 對帳（非幣別標的） | 數量比記錄多 1.5 | `account +1.5`；`adjustment −1.5` |

- **分攤與代付**：種類仍是支出。我自己的份是 `expense`，替別人付的部分是各對象一筆 `dealing`（為正：他們欠我），帳戶損失兩者的總和。別人替我付時沒有帳戶分錄，只有一筆 `dealing`（為負：我欠他），金額是我自己的份的總和；這時不能再有分攤。全部替別人付、自己沒有份的不是支出，是往來（`RecordDealing`）。同一個對象在一筆支出裡只能出現一次。分攤給對象的金額用支出的標的，沒有 `base_amount`。分攤與別人代付的標的必須是幣別（與往來相同，`NewExpense` 回 `ErrNotCurrency`）；沒有分攤、也不是別人代付的支出，標的不限。
- **往來**：標的必須是幣別（`is_currency`），因為往來以雙方結算的幣別計。方向 `paid_out`（往來增加）是帳戶減少、`dealing` 為正；`received`（往來減少）反過來。往來不是支出或收入，不進任何類別。
- **手續費**：隨收入、轉帳、換匯、往來、回饋而來的費用，各自是一對分錄：`account(手續費的帳戶) −金額` 與 `expense(類別) +金額`，`base_amount` 的規則與支出相同。**一律是自己的一對分錄，不併入主金額的帳戶分錄**，即使兩者是同一個帳戶與標的，所以上表轉帳的 A 有兩筆分錄（−1000 與 −15）；手續費的帳戶與標的可以和主交易不同。手續費排在主交易的分錄之後，依輸入的順序。支出的手續費就是另一個類別行，沒有獨立的欄位。手續費的類別必須是支出方向，且不能是系統類別。
- **折扣**只是支出分錄上的 `discount` 欄位，上表折扣那一列的 `account −450` 與 `expense +450` 沒有為折扣多出任何分錄。
- **回饋**是獨立的種類，不沖減支出，也不是收入。點數在結帳時折抵，記成兩筆交易：全額的支出，加上同一個帳戶的一筆回饋；支出裡沒有回饋的成分，點數本身不追蹤（`CONTEXT.md`）。
- **對帳**的差額 = 實際餘額 − 記錄的餘額，帳戶分錄的數量就是差額，所以確認後帳戶等於實際餘額。幣別的差額記成「未記錄」類別的支出（少了）或收入（多了）；非幣別標的只修正數量，用 `adjustment`，沒有 `base_amount`，也不是支出或收入。
- **`Validate`** 對這些種類的檢查：支出是一筆帳戶分錄（或一筆為負的 `dealing`，別人付的時候）加一或多筆 `expense`，再加每個分攤對象一筆為正的 `dealing`；往來是一筆帳戶分錄加一筆 `dealing`，正負相反；回饋是為正的帳戶分錄加為負的 `reward`；對帳是一筆帳戶分錄加對沖它的一筆（「未記錄」的支出為正、「未記錄」的收入為負，或 `adjustment`），正負相反。收入、轉帳、換匯、往來、回饋可以另有手續費：不是 `expense` 的種類裡，為正、沒有折扣的 `expense` 分錄與付它的帳戶分錄（同標的、同數量、為負）是一筆手續費，拿掉手續費後再依種類檢查。有多筆帳戶分錄都可能是付款時，取最後一筆，因為建構函式把手續費放在最後；手續費和主金額一樣大時靠這條規則仍能分辨（有測試）。系統類別只有對帳可以用。

### 各種類的分錄（M1c）

| 種類 | 例子 | 分錄 |
|---|---|---|
| 消費分期（起點） | 刷卡買 12,000，分 12 期 | `instalment(卡) −12000`；`expense(飲食) +12000, base 12000`（沒有帳戶分錄：此刻帳戶一毛也沒扣） |
| 帳單分期（起點） | 把已出的 3,000 帳單轉成 3 期 | `account(卡) +3000`；`instalment(卡) −3000` |
| 每一期（兩種計畫相同） | 本金 1,000，利息 30 | `instalment(卡) +1000`；`account(卡) −1030`；`expense(利息類別) +30, base 30`（沒有利息就沒有第三筆） |
| 買入 | 用 300,000 TWD 買 0.1 BTC | `account(銀行) −300000 TWD`；`conversion +300000 TWD`；`conversion −0.1 BTC`；`account(錢包) +0.1 BTC` |
| 賣出 | 賣 0.04 BTC 得 130,000 TWD | `account(錢包) −0.04 BTC`；`conversion +0.04 BTC`；`conversion −130000 TWD`；`account(銀行) +130000 TWD` |

- **分期是一個計畫（plan）**：一筆起點（origin）加每期一筆（種類 `instalment`，位置 1…N）。起點與各期在建立分期當下一次產生（消費分期是刷卡當下，帳單分期是轉成分期當下），每期的扣款日（該期交易的 `occurred_on`）與金額在那時就定了，之後只能整體改期（`RescheduleInstalment`，見第 5 節；`CONTEXT.md`：分期）。消費分期的起點種類是 `expense`（可有多個類別行與分攤，卡的扣款是全額，含替別人付的部分），帳單分期的起點種類是 `instalment`；起點的 `plan_id` 就是它自己的 id，位置為 0。各期沿用起點的說明、商家、專案、領域與標籤，沒有 `occurred_at`（不再逐日重新帶入專案與領域）；各期的冪等鍵是 `instalment:<起點 id>:<位置>`，由 domain 推出，不由呼叫端給，起點用呼叫端給的鍵。呼叫端給的鍵若以 `instalment:` 開頭，一律回 `ErrReservedIdempotencyKey`：否則那個請求會以「重複」的名義拿到某一期。這個檢查在 `NormalizeIdempotencyKey`，每一種 `Record*`、`SetOpeningBalance` 與 `Reconcile` 的鍵都經過它，先於查鍵與開資料庫交易；各期自己的鍵是在標準化之後才產生的，不受影響。
- **未到期分期（Unbilled Instalment）**：消費分期的起點把全額記進 `instalment` 分錄（為負），不動帳戶；每一期的 `instalment` +本金 把它沖掉，同一筆交易的 `account` −(本金＋利息) 才算進帳戶。所以帳戶餘額從扣款日起才減少，還沒到期的本金單獨留在 `instalment` 帳裡，讀出來是 `Balance.UnbilledInstalment`（為負，與帳戶餘額同號，加起來就是這張卡全部欠的）。`instalment` 帳不進其他統計。消費分期的起點，付款的一邊排在前面（`instalment` 在 `expense` 之前），與 M1b 一致；帳單分期的起點是 `account` 在前，手續費排在最後。
- **驗證**（`NewInstalmentExpense`、`NewBillInstalment`；利息類別由 use case 先載入）：至少一期（`ErrNoPeriods`）；扣款日嚴格遞增（`ErrDueDatesNotIncreasing`），第一期不早於起點的日期（`ErrFirstDueTooEarly`）；本金與利息為正、小數位數不超過標的的 `decimals`，本金加總等於被分期的金額（`ErrPrincipalMismatch`）；`InterestBaseAmount` 只在有利息時給（`ErrInterestBaseAmountWithoutInterest`），外幣的利息與類別行一樣需要它；有任何一期有利息就必須給利息類別（`ErrInterestCategoryRequired`），給了的類別必須未封存、是支出方向、不是系統類別；計畫的標的必須是幣別（`ErrNotCurrency`）；別人替我付的支出不能分期（`ErrInstalmentsWithPaidBy`）。0 或負的利息不收，沒有利息就不給。
- **買賣**：與換匯同一個形狀，差別在被買賣的標的必須是非幣別（`ErrIsCurrency`），付款的標的必須是幣別（`ErrNotCurrency`）；方向只有 `buy`、`sell`（`ErrInvalidTradeDirection`）。M1c 只記數量與金額，不算成本與損益（M5）；可以有手續費，規則同 M1b。賣出比持有的多也照收（不變條件 7）。

## 4. 資料表

主鍵都是應用程式產生的 UUIDv7。金額與數量用 `numeric`，Go 端用 `github.com/shopspring/decimal`（domain 允許 import，需加入 depguard 的 allow list）。

### catalog（migration `0002_catalog.sql`）

- `catalog_area`：加 `archived_at timestamptz`。
- `catalog_project`：加 `starts_on date`、`ends_on date`、`active_from date`、`active_to date`、`default_area_id uuid`（外鍵到 `catalog_area`，同一個 context 內）、`archived_at timestamptz`。檢查：`ends_on >= starts_on`；`active_to >= active_from`；進行期間落在專案期間內（兩者都有值時）；期間與進行期間各自兩端要同時有值或同時為空。
- `catalog_party`、`catalog_tag`：加 `archived_at timestamptz`。

預算欄位不放在 catalog（預算屬於 finance，M1c）。

### finance 參考資料（migration `0003_fin_reference.sql`）

- `fin_instrument`：`id`、`code text UNIQUE`（`TWD`、`USD`、`BTC`、`2330.TW`…）、`name`、`kind`（`fiat`／`crypto`／`stock`／`fund`）、`is_currency bool`、`decimals smallint`（顯示與輸入的小數位數，0–18）、`created_at`、`archived_at`（第 5 節的驗證要求標的未封存）。`is_currency` 與 `kind` 分開：USDT 的 `kind` 是 `crypto`，但 `is_currency` 為真。`kind` 叫 `fiat` 而不叫 `currency`，因為 `CONTEXT.md` 的幣別（Currency）包含 USDT：幣別看 `is_currency`，不看 `kind`。migration 內建 `TWD`（`kind` 為 `fiat`，本位幣，id 固定，`decimals` 取 ISO 4217 的 2，不可封存）。
- `fin_institution`：`id`、`name UNIQUE`、`created_at`、`archived_at`。
- `fin_account`：`id`、`name UNIQUE`、`institution_id` 可為空（外鍵）、`kind`（`cash`／`bank`／`card`／`exchange`／`brokerage`／`wallet`／`loan`）、`created_at`、`archived_at`。帳戶不綁定標的。
- `fin_category`：`id`、`direction`（`expense`／`income`）、`parent_id` 可為空（外鍵，最多兩層，父子的 `direction` 相同）、`name`、`created_at`、`archived_at`；`UNIQUE (direction, parent_id, name)`（`parent_id` 為空時也要唯一，用 `NULLS NOT DISTINCT`）。「最多兩層」與「同方向」由一個複合外鍵 `fin_category_parent_fkey` 保證，它需要兩個產生欄位 `is_top`、`parent_must_be_top`（應用程式不寫入）。M1b 的 migration 內建兩個系統類別「未記錄」（見下）。

### 帳本（migration `0004_fin_ledger.sql`）

- `fin_transaction`：`id`、`kind`（`expense`／`income`／`transfer`／`exchange`／`opening`；M1b 加入 `reward`／`dealing`／`reconciliation`；M1c 加入 `instalment`／`trade`）、`status`（`draft`／`confirmed`／`void`）、`occurred_on date`、`occurred_at timestamptz` 可為空、`description text`、`merchant text`、`project_id uuid`、`area_id uuid`、`idempotency_key text UNIQUE`、`plan_id uuid` 與 `plan_seq integer`（M1c，可為空，見 `0006`）、`created_at`、`confirmed_at`、`voided_at`。
  - `description`、`merchant` 為 `NOT NULL DEFAULT ''`（沒有就是空字串）；`idempotency_key` 為 `NOT NULL`。M1a 沒有 `inbox_item_id` 與 `is_trial`：收件到草稿的連結在 M3 隨 inbox 的 port 加入（inbox 目前沒有能驗證收件 id 的 port），`is_trial` 等用途確定後再以新 migration 加入。
  - CHECK：`kind`、`status` 的值；狀態與時間欄位相符（草稿沒有 `confirmed_at`、`voided_at`；已確認有 `confirmed_at`；作廢兩者都有，因為只有已確認的能作廢）。索引：`(occurred_on DESC, id DESC)`（列表由新到舊）、`project_id`、`area_id`。
- `fin_posting`：`id`、`transaction_id`（外鍵，`ON DELETE CASCADE`）、`seq smallint`、`book`、`account_id` 可為空（外鍵）、`category_id` 可為空（外鍵）、`party_id uuid` 可為空（M1b，catalog 對象的 id，不加外鍵）、`instrument_id`（外鍵）、`quantity numeric NOT NULL`、`base_amount numeric`、`discount numeric`（M1b）、`memo text`；`UNIQUE (transaction_id, seq)`；CHECK：`book` 與必填欄位的對應（第 3 節的表）。索引：`(account_id, instrument_id)`、`(category_id)`、`(party_id)`（M1b）。
  - `memo` 為 `NOT NULL DEFAULT ''`。`book` 與欄位的對應拆成幾個 CHECK（M1c 改寫後的樣子）：`book` 是九個值之一；`account_id` 只有 `account` 與 `instalment` 有，而且這兩個一定有；`category_id` 只有 `expense`／`income` 有；`party_id` 只有 `dealing` 有，而且 `dealing` 一定有；`base_amount` 只有 `expense`／`income`／`reward` 有；`discount` 為空，或在 `expense` 分錄上且大於 0（`conversion`、`opening`、`adjustment` 各欄位皆空，`instalment` 只有帳戶）；另有 `quantity <> 0`，以及 `base_amount` 與 `quantity` 同號。
- `fin_transaction_tag`：`transaction_id`（外鍵，`ON DELETE CASCADE`，捨棄草稿時標籤一起刪）、`tag_id uuid`；主鍵為兩者。
- **平衡檢查**（不變條件 1 的資料庫層）：函式 `fin_assert_transaction_balanced` 與兩個 `DEFERRABLE INITIALLY DEFERRED` 的 constraint trigger：`fin_transaction_balanced`（新增交易時）、`fin_posting_balanced`（分錄新增、修改、刪除時）。資料庫交易提交時，對每一筆受影響且仍存在的交易檢查「至少有一筆分錄，且每個標的的 `quantity` 加總為 0」，違反時以 SQLSTATE `23514`、constraint 名稱 `fin_transaction_balanced` 中止提交。捨棄草稿時分錄連帶刪除，交易已不存在，所以通過。因為交易列與分錄是多條語句，寫入必須在同一個資料庫交易內（第 5 節）。

### 往來、回饋與對帳（migration `0005_fin_dealings.sql`）

M1b 的資料庫變更全在這一個 migration；`0001`–`0004` 已合併，不改。

- `fin_transaction_kind_check`：`kind` 加入 `reward`、`dealing`、`reconciliation`。
- `fin_posting`：加 `party_id uuid`、`discount numeric` 兩個欄位與 `(party_id)` 索引；`fin_posting_book_check` 加入 `dealing`、`reward`、`adjustment`；`fin_posting_base_amount_check` 改為只有 `expense`／`income`／`reward` 有 `base_amount`；新增 `fin_posting_party_id_check` 與 `fin_posting_discount_check`（內容見上）。其他 CHECK 不變。平衡檢查的 trigger 不必改：新的 book 照樣依標的加總。
- **系統類別**：內建兩個頂層類別，名稱都是「未記錄」，一個方向 `expense`、一個方向 `income`，id 固定（`00000000-0000-7000-8000-000000000101`、`00000000-0000-7000-8000-000000000102`），就是 `model.UnrecordedExpenseCategoryID`、`model.UnrecordedIncomeCategoryID` 的值，與 `BaseCurrencyID` 同一個做法，兩邊必須相等（有測試把對帳的分錄存進資料庫，外鍵會擋下不相等的 id）。名稱受 `UNIQUE NULLS NOT DISTINCT (direction, parent_id, name)` 約束：資料庫裡已有任一方向、同名的頂層類別時，這個 migration 在播種之前就以 `cannot apply 0005_fin_dealings: a top-level category is already called 未記錄` 中止並整個還原，HINT 說要先改名（有測試）；沒有這道檢查的話，會是 `fin_category_name_key` 的唯一性錯誤，看不出該怎麼辦。`fin_category_system_not_archived_check` 讓這兩筆不能被封存，use case 也擋（`ErrSystemCategory`）。`fin_category_system_parent_check` 讓它們沒有子類別（`parent_id` 不能是這兩個 id），`NewCategory` 與 `CreateCategory` 也擋（`ErrSystemCategory`）：類別行不能用系統類別，卻可以用它的子類別，一般的支出就會記在「未記錄」底下，繞過下一句的規則（有測試）。一般的類別行與手續費不能用系統類別，只有對帳寫入它們。
- **Down** 還原成 `0004` 的 schema（有測試：與只套用到 `0004` 的資料庫比對 catalog 裡的欄位、約束、索引、trigger 與種子資料），並刪掉兩個系統類別與它們的兩個 CHECK。帳本裡已有 M1b 才有的資料時，Down **中止並報錯，不刪資料**：選「中止」而不是「刪除」，是因為帳本資料不該被 migration 靜默刪掉。算作 M1b 的資料：種類為 `reward`／`dealing`／`reconciliation` 的交易；`dealing`／`reward`／`adjustment` 的分錄；有 `discount` 的分錄；指向系統類別的分錄；以及手續費，也就是不在「支出」交易裡的 `expense` 分錄。要退回，先自行刪除這些交易。放在系統類別底下的類別也一樣：`fin_category_system_parent_check` 在的時候不會有，被拿掉才會；這時刪除系統類別，外鍵會丟出一個看不出原因的錯誤，所以 Down 先自己檢查，以 `cannot roll back 0005_fin_dealings: a category is a subcategory of 未記錄` 中止，不動資料；先移走或刪掉那些類別（有測試）。

### 分期與買賣（migration `0006_fin_instalments.sql`）

M1c 的資料庫變更全在這一個 migration；`0001`–`0005` 已合併，不改。

- `fin_transaction_kind_check`：`kind` 加入 `instalment`、`trade`。
- `fin_transaction`：加 `plan_id uuid`（計畫的起點的 id）與 `plan_seq integer`（在計畫裡的位置：起點 0，各期 1…N），兩者都可為空。約束：`fin_transaction_plan_check`（兩者同時有值或同時為空）、`fin_transaction_plan_seq_check`（位置 ≥ 0）、`fin_transaction_plan_origin_check`（位置為 0 若且唯若 `plan_id = id`）、`fin_transaction_plan_kind_check`（種類 `instalment` 一定有計畫；`expense` 沒有位置或位置為 0；其他種類沒有計畫）、`UNIQUE (plan_id, plan_seq)`（`fin_transaction_plan_id_plan_seq_key`），以及自我外鍵 `fin_transaction_plan_id_fkey`（`NO ACTION`：被某一期指著的交易刪不掉，所以捨棄計畫要先刪各期）。**資料庫保證的只有這些**：兩個欄位同時有值或同時為空、位置不為負、位置為 0 的是 `plan_id` 指向自己的那一筆、種類與計畫欄位的對應、同一個 `plan_id` 的位置不重複，以及 `plan_id` 指向某一筆存在的交易。它**不保證** `plan_id` 指向的是起點（原始 SQL 可以讓一期指向另一期，或指向一筆普通的交易），也不保證起點的計畫欄位沒有被清空（消費分期的起點種類是 `expense`，清空之後只是一筆普通的支出）、各期的位置連續、各期的分錄與金額對得上起點。這些是 domain 的事：`NewInstalmentExpense`、`NewBillInstalment` 一次產生整個計畫，`Transaction.Validate` 檢查每一筆的形狀，use case 在同一個資料庫交易內寫入整個計畫；不另加資料庫機制。
- `fin_posting`：`fin_posting_book_check` 加入 `instalment`；`fin_posting_account_id_check` 改為 `account` 與 `instalment` 的分錄一定有 `account_id`，其他 book 沒有。其他 CHECK 不變：`instalment` 分錄沒有類別、對象、本位幣金額與折扣。平衡檢查的 trigger 不必改。
- **Down** 還原成 `0005` 的 schema（有測試：與只套用到 `0005` 的資料庫比對 catalog 裡的欄位、約束、索引、trigger 與種子資料）。帳本裡已有 M1c 才有的資料時，Down **中止並報錯，不刪資料**，理由同 `0005`：種類為 `instalment`／`trade` 的交易、有 `plan_id` 的交易（消費分期的起點）、有 `instalment` 分錄的交易。訊息是 `cannot roll back 0006_fin_instalments: the ledger holds instalment plans or trades`，HINT 說要先自行刪除那些交易（每種資料各有一個測試案例）。`0005` 以前的資料不擋 Down。

## 5. Use case（M1a、M1b、M1c）

### catalog（`app/catalog`）

port/in：建立、改名、封存、列出、依 id 查詢 —— 領域、專案、對象、標籤各一組；專案另有設定期間、進行期間（傳空值即清除；期間兩端的日期都要給，缺起日或迄日回傳 `ErrPeriodIncomplete`，不會當成 0001-01-01）、預設領域；`ResolveProjectDefaults(date)`：回傳進行期間涵蓋該日期的專案與其預設領域（最多一個；有多個時回傳錯誤由呼叫端處理）。已封存的專案不算；預設領域在設定之後被封存時，不回傳領域。`ResolveProjectArea(projectID)`：回傳專案的預設領域，專案沒有預設領域或預設領域已封存時回傳空值（不是錯誤）；專案不存在或已封存時回傳 `ReferenceError`，與 `VerifyReferences` 相同。「預設領域未封存才帶入」只有這一份實作，`ResolveProjectDefaults` 共用它。另有 `VerifyReferences`：一次檢查一批專案、領域、對象、標籤的 id 是否存在且未封存，供其他 context 在寫入前驗證。

### finance 參考資料（`app/finance`）

port/in：標的、機構、帳戶、類別的建立、封存、列出、查詢。本位幣不可封存；封存頂層類別時其子類別一併封存。

### 帳本（M1a，`app/finance`）

- `RecordExpense`、`RecordIncome`、`RecordTransfer`、`RecordExchange`、`SetOpeningBalance`：輸入為業務用語（帳戶、金額、類別…），由 domain 產生分錄。支出與收入以 `Lines`（類別、金額、本位幣金額、備註）表示，單一類別就是一行，多類別分攤就是多行。全部帶冪等鍵（去掉前後空白後 8 到 128 個字元，與 inbox 相同）；輸出是 `RecordOutput{Transaction, Duplicate}`：鍵已用過時回傳當初記下的交易，`Duplicate` 為真，不再驗證請求，也不比對內容。一律建立為草稿：輸入沒有直接確認的欄位，`TransactionRepo.Insert` 也只收草稿，交易只經由 `ConfirmTransactions` 才生效（`CONTEXT.md`：所有交易都要經過我確認才生效）。匯入如何確認留待 M2 決定。
- 冪等在併發下的做法：鍵在資料庫有唯一約束；use case 先在資料庫交易內查鍵，沒有才驗證並以 `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING` 寫入。同時進來的相同鍵，第二個的 INSERT 會等第一個提交，然後什麼都不寫、回傳 `repo.ErrDuplicateKey`，use case 再讀回第一個寫的交易。
- `ConfirmTransactions(ids)`、`DiscardDraft(id)`、`VoidTransaction(id)`：以 `SELECT ... FOR UPDATE` 鎖住交易列（確認時依 id 排序，避免互相等待）後，由 domain 檢查狀態轉換（草稿→已確認、草稿→刪除、已確認→作廢，其餘回 `ErrNotDraft`／`ErrNotConfirmed`）。`ConfirmTransactions` 全有或全無：先檢查全部，任何一筆不存在或不是草稿就一筆都不確認，錯誤列出所有有問題的。
- `GetTransaction(id)`、`ListTransactions(filter)`（狀態、日期區間、帳戶、專案、領域）：以 `Limit`（預設 50，上限 200）與 `Offset` 分頁，發生日由新到舊、同一天後記的在前；`HasMore` 表示後面還有。use case 向 repo 多要一筆來判斷 `HasMore`；`TransactionRepo.List` 以同一份快照（`REPEATABLE READ` 的唯讀交易，`DB.WithinSnapshot`）讀 id、列、分錄與標籤，所以讀取期間被刪除或改了狀態的交易，不會讓頁面變短而讓 `HasMore` 誤為假，也不會讓不符合篩選條件的列出現在結果裡。
- `GetBalances(accountID?, asOf?)`：各帳戶各標的的餘額，只含已確認、且 `occurred_on` 在該天或之前的分錄（`asOf` 預設為今天，見 M1c）；有已確認分錄的帳戶與標的各一列，依帳戶名稱與標的代碼排序，分錄加總為 0 時仍列出。只有一條查詢（`TransactionRepo.Balances`，帳戶、標的與日期都是它的篩選；M1b 對帳用的 `BalanceAsOf` 併入這一條，不另有第三條餘額查詢）。M1c 每列另有 `UnbilledInstalment`。

驗證：帳戶、類別、標的存在且未封存；類別的 `direction` 與種類相符；專案、領域、標籤經 catalog 的 port 驗證；支出與收入的金額為正；小數位數不超過標的的 `decimals`。

專案與領域的帶入（`CONTEXT.md`：專案有一個預設的領域，底下的交易自動帶入，個別交易可以改）：

- 未指定專案：若 `ResolveProjectDefaults(occurred_on)` 有結果，帶入專案與其預設領域；找到多個進行中的專案時（`AmbiguousProjectsError`）不帶入專案與預設領域，也不算錯。
- 指定專案、未指定領域：不查進行期間，領域帶入該專案的預設領域（經 catalog 的 `ResolveProjectArea`，與 `ResolveProjectDefaults` 同一個規則）；專案沒有預設領域，或預設領域已封存時不帶入領域，也不算錯。
- 指定領域：永遠優先於專案的預設領域，不查預設。

固定扣款的例外在 M3 處理。

交易邊界由 use case 決定：每個 Record*、確認、捨棄、作廢（含驗證所需的讀取與冪等鍵查詢）在同一個資料庫交易內完成，repo 不自行開交易，唯一的例外是 `TransactionRepo.List` 的唯讀快照。需要一個 `pkg/postgres` 的交易輔助（透過 context 傳遞 `pgx.Tx`）；use case 經 `usecase/port/out/tx.Transactor`（只有 `WithinTx`）使用它，由 `cmd/` 注入 `*postgres.DB`。巢狀的 `WithinTx` 用 savepoint（明確下 `SAVEPOINT`、`RELEASE SAVEPOINT`、`ROLLBACK TO SAVEPOINT`）：內層回傳錯誤時，內層的寫入一律還原，外層交易仍可繼續使用；這包括內層的 context 在提交前已結束，以及內層忽略失敗的語句後回傳 nil（交易已在失敗狀態，savepoint 放不掉）。`TransactionRepo.Insert` 以多條語句寫入交易、分錄與標籤，必須在資料庫交易內呼叫，否則平衡檢查會在第一條語句提交時拒絕。

### 帳本（M1b，`app/finance`）

- `RecordExpense`：`AccountID`（指標）與 `PaidByPartyID` 兩者擇一，兩者都給回 `ErrTwoPayers`，都沒有回 `ErrNoPayer`。`Shares []Share{PartyID, Amount, Memo}` 是替別人付的部分，用支出的標的；`Lines` 只放自己的份；帳戶損失所有 `Lines` 與 `Shares` 的總和。同一個對象只能有一份（`ErrDuplicateParty`）。`PaidByPartyID` 時不能有 `Shares`（`ErrSharesWithPaidBy`）。沒有自己的行、只有分攤的不是支出（`ErrNoLines`，訊息指向 `RecordDealing`）。`Line.Discount` 記下店家當場折掉的金額，只有支出的行可以有（收入的行給了回 `ErrDiscountNotAllowed`）。有 `Shares` 或 `PaidByPartyID` 時，標的必須是幣別（`ErrNotCurrency`），與 `RecordDealing` 相同：分攤與代付都是對象欠我、或我欠對象，往來以幣別計；沒有分攤、也不是別人代付的支出，標的不限（用 BTC 付一杯咖啡仍是支出）。
- `Fees []Fee{AccountID, InstrumentID, CategoryID, Amount, BaseAmount, Memo}`：`RecordIncome`、`RecordTransfer`、`RecordExchange`、`RecordDealing`、`RecordReward` 的輸入都有；支出的手續費就是另一個類別行。分錄的形狀見第 3 節。類別必須是支出方向（`ErrWrongCategoryDirection`），不能是系統類別；`BaseAmount` 的規則與類別行相同。錯誤訊息會說是第幾筆手續費。
- `RecordDealing{AccountID, PartyID, InstrumentID, Amount, Direction}`：`Direction` 是 `paid_out` 或 `received`（`ErrInvalidDealingDirection`）；標的必須是幣別（`ErrNotCurrency`）。`RecordReward{AccountID, InstrumentID, Amount, BaseAmount}`。
- 對象經 catalog 的 `VerifyReferences`（`PartyIDs`）驗證存在且未封存，錯誤是 catalog 的 `*ReferenceError`：分攤的對象、別人替我付時的那個對象、往來的對象都是。
- `Reconcile{帳戶, 標的, ActualBalance, DifferenceBaseAmount?}` → `ReconcileOutput{RecordedBalance, Difference, Transaction, Duplicate}`：
  - `OccurredOn` 是觀察餘額的那一天（只看日期）。記錄的餘額 = 該帳戶、該標的、`occurred_on` 在該天或之前的已確認分錄加總：當天算入，隔天不算（`TransactionRepo.Balances` 以 `Day` 取該帳戶、該標的的那一列，沒有分錄就是 0，有測試）。`Difference` = 實際 − 記錄。
  - 有草稿在該帳戶、該標的上有分錄、日期在該天或之前時拒絕，回 `*PendingDraftsError`（`errors.Is` 為 `ErrPendingDrafts`，帶草稿的張數，一張草稿只算一次；`TransactionRepo.PendingDrafts`）：記錄的餘額要等它們確認或捨棄才知道。這也擋下上一次對帳的草稿還在時又對帳一次，否則差額會記兩次。
  - **同一個帳戶的對帳一次只做一個。** 資料庫交易的第一個語句鎖住帳戶列（`AccountRepo.Lock`：`SELECT id FROM fin_account WHERE id = $1 FOR NO KEY UPDATE`，交易結束才放），之後才查鍵、數草稿、讀餘額。後來的對帳在鎖上等，等到前一個提交（或回滾）才往下讀，讀到的就是前一個寫的草稿，所以被 `*PendingDraftsError` 擋下。沒有鎖的話，同時進來、鍵各不相同的對帳在 `READ COMMITTED` 下讀到同一個餘額與同一個「沒有草稿」，每一個都記一筆差額（併發測試在沒有鎖時寫出 4 張草稿，確認後餘額是 1920，不是 1980）。這個做法靠交易是 `READ COMMITTED`：鎖之後的每個語句都看得到前一個對帳提交的內容；`REPEATABLE READ` 的快照在第一個語句（也就是鎖）就定了，鎖之後讀到的仍是舊的資料，擋不住重複。用 `FOR NO KEY UPDATE`，不用 `FOR UPDATE`：記到這個帳戶的分錄，外鍵取的是 `FOR KEY SHARE`，與前者不衝突，所以對帳不會擋住別的交易記帳（有測試）。鎖的是帳戶，不分標的：比檢查需要的粗，但同一個帳戶同時對兩個標的對帳不值得做得更細。
  - **鎖在查冪等鍵之前，不在之後。** 與還在跑的那一次同鍵的重送，先查鍵的話查不到（前一次還沒提交），接著等鎖，等到時前一次的草稿已經在了，重送就被它擋下（`*PendingDraftsError`），而不是拿到那一次的結果。先鎖，重送等到前一次提交之後才查鍵，查到就回傳那一次的草稿（`Duplicate`）（併發測試涵蓋）。帳戶不存在時鎖不算失敗（略過 `ErrNotFound`）：用過的鍵不管它寫的是哪個帳戶都先被回答，帳戶在後面驗證請求時才檢查。
  - 差額為 0：不寫任何交易，`Transaction` 為空。**M1b 不存「差額為 0 的對帳」這種紀錄**；要保留對帳的日期與結果，等需要時再加。
  - 幣別：差額記成「未記錄」類別的支出（比記錄少）或收入（比記錄多）。外幣必須給 `DifferenceBaseAmount`（正數，不論差額的正負）；沒給回 `*DifferenceBaseAmountRequiredError`（`errors.Is` 為 `ErrBaseAmountRequired`），帶著差額與標的代碼，呼叫端算出本位幣金額後帶著再呼叫一次。本位幣可以不給，給了必須等於差額的絕對值。
  - 非幣別標的：用 `adjustment` 只修正數量；給了 `DifferenceBaseAmount` 回 `ErrBaseAmountNotAllowed`。
  - `ActualBalance` 可以是負的（欠款的帳戶），小數位數不超過標的的 `decimals`。結果與其他交易一樣是草稿，確認後帳戶的餘額就是 `ActualBalance`（有測試）。
  - 冪等與其他 Record* 一樣：鎖了帳戶之後、其他任何東西之前先查鍵，鍵已用過時回傳當初的交易（`Duplicate` 為真），不看帳戶有沒有被封存、有沒有草稿在等（當初那張草稿自己就是在等的草稿）；這時不計算 `RecordedBalance` 與 `Difference`。
  - **已知的限制**：對帳草稿做出之後、確認之前，才記下並確認的、日期在對帳那天或之前的交易，會讓這張調整過時。M1b 不偵測。
- `GetDealings(partyID?)`：每個對象每個標的一列，已確認的 `dealing` 分錄加總（`paid_out` 與 `received` 互相抵銷），加總為 0 的列仍列出。`TransactionRepo.Dealings` 依對象 id、標的代碼排序；對象的名稱在 catalog，所以 use case 再依 catalog 列出對象的順序（`ListParties`，含已封存的：他們欠我的仍然算）重排，同一個對象內保持標的代碼的順序。這樣不必新增 catalog 的 port 方法，順序也與 catalog 的名稱順序一致。有 `dealing` 分錄的對象在 catalog 找不到時回 `ErrNotFound`，不猜名稱。給了 `partyID` 時只有一個對象，沒有順序要排：不呼叫 `ListParties`，也不重排，repo 的順序（標的代碼）就是結果的順序；先用 catalog 的 `VerifyReferences`（`PartyIDs` 只有這一個）確認這個對象，同樣不必新增 catalog 的 port 方法。`*ReferenceError` 的 `Archived` 分得出「找不到」與「已封存」：已封存的對象放行（他們欠我的仍然算）；找不到的回 finance 的 `ErrNotFound`（前面加上 `party <id>:`），與上一句有 `dealing` 分錄的對象找不到是同一個錯誤，呼叫端用 `errors.Is(err, model.ErrNotFound)` 就認得，不必認 catalog 的 `*ReferenceError`。找不到時不讀 repo：問一個不存在的對象是錯誤，不是「沒有往來」。catalog 的其他錯誤（例如連線失敗）不當成找不到，原樣回傳（加上 `check party <id>:`）。`GetDealingsInput.PartyID` 的註解寫明了這個錯誤。
- `ListTransactions` 的篩選多一個 `PartyID`：有該對象 `dealing` 分錄的交易（含分攤，以及別人替我付的支出）。
- `ArchiveCategory` 對系統類別回 `ErrSystemCategory`；`CreateCategory` 以系統類別為父類別時也回 `ErrSystemCategory`（系統類別沒有子類別）。資料庫的 CHECK（`fin_category_system_not_archived_check`、`fin_category_system_parent_check`）是最後一道。

驗證沿用 M1a：參照存在且未封存（對象經 catalog）、金額為正、小數位數不超過標的的 `decimals`、冪等不變、一律是草稿、全部在同一個資料庫交易內。

### 帳本（M1c，`app/finance`）

**「今天」與計入帳戶的時間。** 餘額與往來只計 `occurred_on` 在那一天或之前的已確認分錄：日期在未來的交易，確認了也要到它的日子才算（M1a、M1b 的讀取同樣如此，這是既有行為的改變）。分期每一期的扣款日就是這樣生效的，沒有排程或背景工作去「入帳」。「今天」來自注入的時鐘 `usecase/port/out/clock.Clock`（只有 `Now()`），use case 取它的日期（`model.DateOf`，沿用時鐘自己的時區）；`cmd/` 以 `pkg/clock` 實作，時區是環境變數 `MAXOS_TIMEZONE`（IANA 名稱，預設 `Asia/Taipei`），啟動時以 `time.LoadLocation` 驗證，不合法就失敗並指名這個變數；二進位內嵌 `time/tzdata`，沒有時區資料庫的執行環境（Alpine 映像）也載得到。測試給假的時鐘，不靠真實日期，也不 sleep。要「今天」的讀取是 `GetBalances`、`GetDealings`（兩者的 `AsOf` 可指定日期，不給就是今天）與 `ListInstalmentPlans`。`Reconcile` 不用時鐘：它看的是呼叫者給的 `OccurredOn`，所以日期在對帳那天之後才到期的各期草稿，不會擋下對帳。

- `RecordExpense` 的 `Instalments`：給了就是消費分期。`InstalmentSchedule{Periods []InstalmentPeriod{DueOn, Principal, Interest?, InterestBaseAmount?}, InterestCategoryID?}`；use case 為每期產生 UUIDv7、載入並驗證利息類別，其餘由 domain 檢查（第 3 節）。`AccountID` 是被分期的卡；`PaidByPartyID` 不能分期。`RecordBillInstalment{帳戶, 標的, Principal, Schedule, Fees}` 是帳單分期。兩者的輸出都是起點（`RecordOutput`）。
- 起點與各期在同一個資料庫交易內寫入，起點先寫（各期的外鍵指向它）；整個計畫只有起點的那一個冪等鍵：鍵已用過時回傳當初的起點（`Duplicate`），不再寫各期（各期的鍵由起點推出，也不會撞到別的交易）。
- `ConfirmTransactions`、`DiscardDraft`、`VoidTransaction` 以計畫為單位：給起點的 id，帶著整個計畫。期也是各自的交易，`ListTransactions` 會列出，所以「確認列出的全部草稿」會把起點與各期的 id 一起送來：**起點也在請求裡的期，由起點的展開涵蓋，不算錯，也不重複處理**（每一筆只確認一次，整個呼叫仍是一個資料庫交易）；起點不在請求裡的期回 `ErrPartOfPlan`（錯誤列出所有這樣的期），而且不鎖那一期，即使請求裡有別的計畫的起點。`ConfirmTransactions` 的輸出是請求的每個 id 各一筆（重複的 id 算一次），依請求的順序，是確認之後的樣子：給了的期在輸出裡，沒給的期不在（只給起點就只有起點一筆，各期用 `GetInstalmentPlan` 讀）。`DiscardDraft` 與 `VoidTransaction` 只收一個 id，起點不可能與期同在一個請求裡，所以期的 id 一律是 `ErrPartOfPlan`。捨棄先刪各期、再刪起點（自我外鍵）；作廢給起點，各期已經到期也可以，作廢後整個計畫從餘額與計畫列表消失。
- **鎖的順序**：先不鎖地讀一次，把請求的 id 分類（一般交易、起點、期）；然後所有呼叫（確認、捨棄、作廢、改期）都照同一個順序鎖：先是不是期的那些，也就是起點與一般交易，依 id 升冪 `SELECT ... FOR UPDATE`；再來是每個已鎖住的起點的計畫，它的各期依 id 升冪（起點也在請求裡的期併入這個展開，不另鎖）。一個計畫的各期只有握著它起點的那個呼叫會鎖，所以各期的鎖永遠不必等，也不會成為環的一部分；兩個有重疊的呼叫在起點與一般交易上依同一個 id 順序鎖，所以不會死結；後到的在鎖上等，等到時狀態已變，回 `ErrNotDraft`／`ErrNotConfirmed`。各期要等起點鎖到手才查：改期會把各期換成新的，起點鎖到之前讀到的各期，鎖到時可能已經不在。分類到鎖之間計畫被刪掉了，也回錯誤而不是靜默略過。use case 的測試以假的 repo 紀錄鎖的順序（起點與一般交易升冪，然後各計畫的各期升冪；被拒絕的期沒有鎖；每一筆只鎖一次）與分類後才不見的期；對真實資料庫的併發測試見第 6 節。
- `GetInstalmentPlan(planID)` → `InstalmentPlan{Origin, Periods}`（各期依位置）；它讀的是存起來的交易，沒有依日期算的金額，所以不看今天；id 不是起點（一般交易、期、不存在）回 `ErrNotFound`。`ListInstalmentPlans{AccountID?, IncludeFinished}` → `InstalmentPlanSummary{PlanID, AccountID, InstrumentID, Principal, Charged, Remaining, NextDueOn}`：只含已確認、起點日期在今天或之前的計畫，以今天為準。計畫從起點的日期起算，與 `GetBalances` 一樣（起點的 `instalment` 分錄那天才進餘額）：起點日期在今天之後的，兩邊都沒有。三個金額都是**大小**，不為負：`Charged` = 已到期各期的本金（不含利息），`Remaining` = `Principal` − `Charged`，也就是這個計畫還沒入帳的本金。`Remaining` 與 `Balance.UnbilledInstalment` 是同一個量、符號相反：後者是 `instalment` 分錄的加總，為 0 或負（−11,000 表示還有 11,000 要在之後的各期入帳），一個帳戶的 `UnbilledInstalment` 等於它所有計畫的 `Remaining` 之和取負（有測試逐日比對）。`NextDueOn` 為空表示已全部到期，預設不列已全部到期的（`IncludeFinished` 才列）；起點日由新到舊。以一條彙總查詢算出，所以是同一份快照。
- `RescheduleInstalment{PlanID, Schedule}` → `InstalmentPlan`：把計畫的排程整個換成新的。`Schedule` 與記錄時是同一個 `InstalmentSchedule`，檢查也一樣（日期遞增、第一期不早於起點、本金為正、利息與利息類別、本位幣金額），所以各期的扣款日、本金、利息、利息類別與**期數**都可以變。不變的有兩樣：起點從頭到尾不動；新排程的本金加總必須等於計畫的本金（起點的 `instalment` 分錄），否則回 `*PrincipalMismatchError`（`errors.Is` 為 `ErrPrincipalMismatch`，帶著兩個金額：排程加起來的與計畫的）。總額變了就是購買變了，要作廢重記；提前清償就是改期：把剩下的本金放進清償那天的一期。草稿與已確認的計畫都可以，已作廢的回 `ErrPlanVoid`；id 不是起點（一般交易、期、不存在）回 `ErrNotFound`。已經到期的期也可以改：機構實際扣的未必是排的。
  - **這是「已確認的交易不能修改」的唯一例外**（不變條件 4）：被改的是排程，也就是機構何時、扣多少，那會變；購買（起點）與總額不變。做法是刪掉舊的各期、寫入新的各期，所以**各期的交易 id 會變**（鍵 `instalment:<起點 id>:<位置>` 不變，位置 1…N），舊的各期不再存在。新的各期用記錄時的同一份程式碼做（`InstalmentSchedule.periods`）：沿用起點的說明、商家、專案、領域與標籤。新的各期取計畫的狀態：草稿計畫的是草稿，已確認計畫的是已確認，`confirmed_at` 是改期當時的資料庫時間。寫入仍是「先寫成草稿、再確認」：`Insert` 只收草稿，沒有放寬，確認走 `ConfirmTransactions` 同一條路（`SetStatus`），資料表的狀態與時間 CHECK 照常把關，平衡檢查在提交時檢查每一筆新的期。
  - 一個 `WithinTx`：先鎖計畫（上面的鎖的順序），鎖住之後重讀計畫，狀態與有沒有被刪都在鎖下檢查，由 domain（`InstalmentPlan.Reschedule`）做出新的各期，通過了才寫（刪、寫、確認）；被拒絕的什麼都不寫。沒有冪等鍵：同樣的請求做兩次得到同樣的排程，第二次把各期重寫一次，沒關係。
  - 餘額、未到期分期、計畫列表與對帳都是依日期算的，所以改期之後各期從新的日子起算：一期從未來移到今天或之前，帳戶餘額多了它的本金與利息，未到期分期少了本金，反過來也一樣；沒有利息的期移動時，兩者加起來不變。草稿的期移到對帳那天或之前，那天的對帳就被它擋下（`ErrPendingDrafts`），移回去就不擋。這些都不特別處理，也沒有快取。
- `RecordTrade{Direction, AccountID, InstrumentID, Quantity, CashAccountID, CurrencyID, Amount, Fees}`：`Direction` 是 `buy` 或 `sell`；`AccountID` 持有被買賣的標的，`CashAccountID` 收付金額。
- **已知的限制**：被分期的總額不能改（改了就是改了那筆購買：作廢整個計畫、重記）；提前清償就是改期，把剩下的本金放進清償那天的一期（`RescheduleInstalment`）。各期是一般的交易，會出現在 `ListTransactions`（可用 `PlanID` 篩選整個計畫）；不自動確認各期。

## 6. 測試

- domain：分錄產生與不變條件（每個種類、外幣、多類別、不平衡時拒絕）。
- use case：以假的 port 測驗證規則與冪等。
- postgres adapter：整合測試，含 CHECK 限制、平衡檢查（不平衡的交易在提交時被拒絕）、餘額查詢只計已確認、草稿確認後餘額改變、作廢後餘額還原、捨棄草稿連分錄一起刪、全有或全無、併發重複冪等鍵。冪等與全有或全無的測試在 repo 層（`WithinTx` 加 `TransactionRepo`）：adapter 依 depguard 不能 import use case。
- `pkg/postgres`：`WithinTx` 的整合測試，含巢狀 savepoint 的各種失敗（內層回傳錯誤、內層 context 在提交前已結束、內層忽略失敗的語句後回傳 nil、panic、多層）：內層的寫入還原，外層交易仍可繼續使用；`WithinSnapshot` 讀到的是開始時已提交的資料。
- `TransactionRepo.List` 的快照：以 pgx tracer 在列 id 的語句之後、讀列的語句之前，從另一條連線刪除或改狀態，驗證頁面仍是列 id 時看到的那一頁；不用 sleep，所以每次執行都一樣。
- M1b domain：第 3 節 M1b 表的每一種分錄（逐筆比對分錄），外幣分攤；拒絕的情形（對象重複、兩種付款人、別人付時有分攤、沒有自己的行、非支出的折扣、手續費用收入類別、系統類別、系統類別底下的子類別、非幣別的往來、分攤或別人代付用非幣別標的（沒有分攤的非幣別支出仍可以）、對帳的 `DifferenceBaseAmount`）；手續費與主金額一樣大時仍認得出手續費；`Validate` 對新種類與新分錄的各種錯誤。
- M1b use case：以假的 port 測驗證（對象經 catalog、手續費各自載入）、冪等、對帳的各個分支（差額 0、少、多、非幣別、缺本位幣金額、有草稿在等）與以日期計的餘額、`GetDealings` 的順序，以及給對象時只用 `VerifyReferences` 問那一個對象（找不到回 `ErrNotFound` 且不讀 repo，已封存的可以，catalog 的其他錯誤不當成找不到；假的 catalog 照真的回 `*ReferenceError`，以 `Archived` 分出找不到與已封存）；對帳先鎖帳戶、再查鍵、再數草稿與讀餘額（假的 repo 與假的帳戶共用一份呼叫紀錄，驗證順序），鎖失敗就停，用過的鍵不管帳戶存不存在都先回答。假的 repo 的 `Balances`、`PendingDrafts` 依記憶體裡的交易照真實語意計算，所以「對帳草稿確認後餘額等於實際餘額」在 use case 層就能驗證。
- M1b postgres adapter：新的 CHECK 擋下錯誤的 book 與欄位組合；新的 book 仍受平衡檢查；`Dealings` 只計已確認、抵銷往來增減、加總為 0 仍列出；`Balances` 以日期取（當天算入、隔天不算，草稿、作廢與其他帳戶、標的不算）；`PendingDrafts`（一張草稿只算一次）；`PartyID` 篩選；系統類別的 id、不可封存與沒有子類別；`AccountRepo.Lock`（不存在的 id、鎖住時另一個鎖被擋、外鍵的 `KEY SHARE` 不被擋；用 `NOWAIT`，所以不必等）。
- migration：`migrations/` 的整合測試把 `0005` 往下再往上，與只套用到 `0004` 的資料庫比對 catalog；帳本裡有 M1b 的資料時 Down 中止且不動資料；有類別放在系統類別底下時（要先拿掉 CHECK 才放得進去）Down 以自己的訊息中止、不動資料，而不是丟出外鍵錯誤；已有同名的頂層類別時 Up 以自己的訊息與 HINT 中止並整個還原，而不是丟出唯一性錯誤。
- `cmd/` 的整合測試：真的 Fx 模組（`CatalogModule`、`FinanceModule`）加真的 PostgreSQL，把分攤、往來增減、回饋與對帳走過一遍（確認後 `GetDealings` 與餘額，對帳少與多的草稿確認後餘額等於實際餘額；`GetDealings` 問 catalog 沒有的對象回 `ErrNotFound`，問已封存的對象不出錯）。adapter 不能 import use case，use case 也不能 import adapter，只有 `cmd/` 同時知道兩邊，所以端到端的測試放在這裡。
- `cmd/` 的併發測試：同一個帳戶的對帳同時進來，鍵各不相同時只寫一張草稿、其餘回 `ErrPendingDrafts`，確認後餘額等於實際餘額；鍵相同的重送回傳第一個的草稿，不被那張草稿擋下。不靠 sleep 等某個時間點：pgx tracer 把第一個呼叫停在數完草稿之後，其他呼叫在那時才啟動，然後每隔 5 毫秒問一次 PostgreSQL（`pg_stat_activity` 的 `wait_event_type = 'Lock'`）每個呼叫是已經結束、還是在等鎖（tracer 記下每個呼叫用的後端，所以對得上是哪一個呼叫），都有了答案才放第一個呼叫走，所以結果每次都一樣。這個輪詢有期限（20 秒）：到了還有呼叫既沒結束也沒在等鎖，測試就失敗並指出是哪幾個；每個呼叫自己也有期限，卡在別處的呼叫會以錯誤結束，不會讓測試掛到 `go test -timeout`。沒有鎖時，兩個測試都會失敗；鎖放在查鍵之後時，第二個會失敗。
- M1c domain：消費分期與帳單分期的起點與每一期的分錄（逐筆比對）、利息與外幣利息、各期沿用起點的欄位、冪等鍵的推導；拒絕的情形（沒有期、扣款日不遞增、第一期早於起點、本金加總不符、利息沒有類別或類別是系統類別、收入方向、已封存、沒有利息卻給 `InterestBaseAmount`、別人替我付、非幣別的標的）；`Validate` 對計畫欄位與分期分錄的各種錯誤；買賣的兩個方向與各種拒絕。
- M1c use case：以假的 port 與假的時鐘測驗證各期的 id 與鍵、起點先於各期寫入、重送不重複、確認／捨棄／作廢以計畫為單位（期的 id 被拒且不上鎖、鎖依 id 升冪、分類後才不見的期）、`GetBalances` 與 `GetDealings` 的 `AsOf` 與預設（今天）、`ListInstalmentPlans` 的篩選與順序、`RecordTrade`。
- M1c postgres adapter：`instalment` 分錄的 CHECK 與平衡檢查；計畫欄位的各個 CHECK、`UNIQUE` 與外鍵；起點在各期還在時刪不掉、先刪各期可以；計畫寫入全有或全無；`GetPlan` 依位置讀回、不是起點就是 `ErrNotFound`；餘額與未到期分期隨日期變化（購買前、購買當天、第一期前一天、扣款日當天、最後一期之後；草稿與作廢的計畫不算）；日期在未來的一般交易與往來到那天才算；`InstalmentPlans` 的本金、已入帳（不含利息）、下一個扣款日、已結清、帳戶篩選、順序；`PendingDrafts` 只在各期到期那天起才算；買賣的餘額與平衡檢查。
- migration：`0006` 往下再往上，與只套用到 `0005` 的資料庫比對 catalog；Down 對各種 M1c 的資料各有一個拒絕案例，且不動資料；`0005` 以前的資料不擋 Down。`0005` 的測試改成先退到 `0005` 再開始。
- `pkg/config`：`MAXOS_TIMEZONE` 合法、沒設（用預設）與非法（錯誤指名這個變數）。
- `cmd/` 的端到端測試（M1c）：真的模組加真的 PostgreSQL 與可控的時鐘，走過 12 期分期（確認起點、期被拒、各個日子的餘額與未到期分期、計畫列表與已結清的、作廢起點）、買入再賣出、日期在未來的支出到那天才算、對帳不被尚未到期的各期草稿擋下。
- M1c 審查後的修正：use case：起點也在請求裡的期由起點涵蓋、只確認一次，輸出依請求的順序、重複的 id 算一次，別的計畫的起點在請求裡也不能讓這一期通過；每個 `Record*`、`SetOpeningBalance` 與 `Reconcile` 對 `instalment:` 開頭的鍵回 `ErrReservedIdempotencyKey`，不查鍵、不開資料庫交易。domain：`NormalizeIdempotencyKey` 只擋這個前綴（不擋含有它的鍵、不同大小寫的鍵），各期自己的鍵照常產生。postgres adapter：起點日期在那天之後的計畫不列，`InstalmentPlans` 與 `Balances` 逐日一致。`cmd/`：列出全部草稿、一次確認，計畫只入帳一次。
- M1c 改期：domain：新的各期照記錄時的做法（逐筆分錄、沿用起點的欄位、鍵與位置）、起點與被呼叫的計畫都不變、所有排程的拒絕（沒有期、日期、本金、利息與類別，總額不符且帶著兩個金額）、已作廢的計畫、各期與起點狀態不一致、標的不符、沒有 `instalment` 分錄的起點。use case：草稿與已確認的計畫（新各期的狀態與 `confirmed_at`）、舊各期已刪、起點不動、一個資料庫交易、鎖的順序、鎖住之後才發現作廢或被刪、各種拒絕都什麼也不寫、同一排程做兩次結果相同。postgres adapter：刪、寫、確認在一個交易裡取代排程，草稿與已確認的都行，餘額隨新的日子變，一期不平衡時整個取代還原、舊排程原樣。`cmd/` 端到端：12 期的計畫確認、三期過去後把剩下的 9,000 改成下個月一期（利息也改）：餘額、未到期分期、計畫列表與利息支出都對，總共欠的不變；一期在今天前後來回移動；作廢後拒絕；總額不符的錯誤帶著兩個金額；草稿的期移到對帳那天前後。
- 計畫的併發（`cmd/finance_plan_concurrency_integration_test.go`，真的 PostgreSQL）：要看某一種交錯時，交錯是做出來的，不靠運氣：pgx tracer 把第一個呼叫停在它的第一個鎖之後，其他的在那時才啟動，每隔 5 毫秒問 PostgreSQL 它們是已經結束還是在等鎖（有 20 秒的期限），都有了答案才放第一個走。兩個呼叫以相反的順序確認同樣兩個計畫：第一個全部確認，第二個等它、然後回 `ErrNotDraft`，沒有 `40P01`。改期握著起點的鎖時，確認或作廢要等它，等到時動的是新的各期（舊的各期已不在）。要看的是「任何交錯都對」時，跑固定的回合數（20），呼叫同時啟動，沒有 sleep，`-race` 乾淨：兩個計畫以相反順序確認、同時有人捨棄一個、作廢另一個；同一個計畫的兩個確認、一個作廢、一個捨棄；計畫確認與同一帳戶的對帳（對帳不是被草稿擋下，就是算到了那一期，不會拿過時的餘額）；隨機挑的多個計畫與一般草稿一起確認（含起點旁的期）；兩個改期與確認、作廢重疊。每回合檢查：沒有資料庫錯誤（尤其是死結 `40P01`）、一個計畫只有一種狀態、每一筆只被確認一次（tracer 數 `SetTransactionStatus` 到 `confirmed` 的次數）、各呼叫成功與否是某個執行順序留得下的。在副本上弄壞鎖時這些測試會失敗（不在程式碼裡）：起點與一般交易不依 id 排序，相反順序的測試以 `40P01` 失敗；拿掉 `FOR UPDATE`，或讓作廢先鎖一期再鎖起點，對同一個計畫的測試失敗。
