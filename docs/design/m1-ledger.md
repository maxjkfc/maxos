# M1 帳本設計

M1（issue #4）的資料模型與分批計畫。用語以 `CONTEXT.md` 為準，分層與依賴以 `docs/architecture-conventions.md` 為準。本檔只記錄 M1 的設計決定；實作完成後與程式碼不一致之處以程式碼為準，並回頭修正本檔。

## 1. 分批

| PR | 內容 |
|---|---|
| M1a | `catalog` context（領域、專案、對象、標籤）；`finance` 的參考資料（標的、機構、帳戶、類別）；帳本核心：支出、收入、轉帳、換匯，草稿與確認，餘額查詢 |
| M1b | 往來與分攤、回饋、手續費與折扣、對帳與「未記錄」 |
| M1c | 分期與利息、買賣（只記數量與金額，成本計算在 M5）、預算與分配。分兩個 PR：第一個是分期、買賣與「今天」的判斷（日期在未來的交易到那天才計入），本檔的 M1c 段落記的就是這些；第二個是預算與分配，見第 4 節的 `0007` 與第 5 節的「預算與分配」 |
| M1d | 信用卡帳單：帳戶的結帳日與繳款日、交易的入帳日，以及依帳單檢視刷卡的消費（唯讀；帳單由交易算出，不另存）。資料表見第 4 節的 `0008`，規則與 use case 見第 5 節的「信用卡帳單」 |

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
3. `expense`、`income`、`reward` 的分錄必須有 `base_amount`（本位幣 TWD 的金額，符號與 `quantity` 相同，小數位數不超過本位幣的 `decimals`）。標的為 TWD 時等於 `quantity`（呼叫端若給了，必須相等）；否則由呼叫端提供交易當天的金額，寫入後不再變動（唯一的例外：分期計畫改期時帶 `ChangePast`，已到期各期的利息支出與它的本位幣金額會被新排程重寫，見第 4 點）。M1a 只收金額，每個類別行各帶一個，不收匯率，換算由呼叫端負責。M1 不查詢匯率來源。往來（`dealing`）與 `adjustment` 沒有 `base_amount`：往來以雙方結算的幣別計，只在算淨資產時才換算。
4. 只有 `status = confirmed` 的交易計入餘額與任何統計。草稿不修改（只有入帳日可以改，見第 8 點）：要更正就捨棄後重記（M1a 沒有修改草稿的 use case）；已確認的交易不能修改內容，只能作廢（`void`）後重記（入帳日不算內容，見第 8 點）。**唯一的例外是分期計畫的排程**：計畫的各期（扣款日、本金、利息、期數）可以整體換成新的（`RescheduleInstalment`，第 5 節），草稿與已確認的計畫都行，因為各期是機構何時、扣多少，那會變；起點與被分期的總額不在此限。這個例外有多大，分成三點：
   - **排程是整個換掉的**：已經到期的各期也在新排程裡，要原樣再送一次。提前清償就是把到期的各期照舊送上，再把剩下的本金放進一期。
   - **預設過去是固定的。** 過去的期是計畫現在的各期中扣款日嚴格早於今天的（今天以時鐘為準，今天到期的不算）。沒有 `ChangePast`（預設 false）時，這些期不能少、不能多、不能被改：日期、本金、利息、利息的本位幣金額與順序都照舊，有利息時利息類別也不能換；否則回 `*PastPeriodsChangedError`（`ErrPastPeriodsChanged`）並指出是哪幾期，什麼都不寫。草稿的計畫也一樣。
   - **帶 `ChangePast` 時，過去也能改，代價要知道**：已到期各期的利息支出與它的本位幣金額會變（所以第 3 點的「寫入後不再變動」有這一個例外）；過去的帳戶餘額與未到期分期會變（餘額依日期算，各期從新的日子起算）；已確認的、某個過去日子的對帳會因此過時，而且**不會被偵測**（與對帳那一節 M1b 已知的限制同一類：對帳那天或之前的交易事後有變，那張調整就不對了）。
5. 帳戶的餘額 = 該帳戶所有已確認分錄的 `quantity` 依標的加總。不另存餘額欄位。
6. **折扣**只留作記錄：`discount` 只在 `expense` 分錄上、必須大於 0、小數位數不超過標的的 `decimals`。`quantity` 是實付金額，折扣不是分錄，也不進任何餘額或統計（`CONTEXT.md`：支出以實付金額計）。
7. **帳本不檢查餘額是否足夠。** 賣出比持有的多、從帳戶付款付到負數，都照收，餘額可以是負的；這是設計，不是漏洞：帳戶的餘額就是分錄的加總（第 5 點），不是上限（信用卡本來就是負的）。`Validate` 與資料庫都沒有這種檢查，要不要攔是呼叫端的事。
8. **入帳日不是第 4 點的例外，也不動第 3 點。**交易可以有一個入帳日（`posted_on`，M1d）：機構把這筆交易放進哪一張帳單的那一天，沒有就是交易自己的日期。它只改變交易在信用卡的哪一張帳單上：餘額、預算與往來在同一天計入同樣的金額，不論有沒有入帳日，日期篩選也照舊只看 `occurred_on`。設定、更改或清除它不動任何分錄、金額、日期或本位幣金額，所以草稿與已確認的交易都可以改（作廢的不行）。要這樣，是因為銀行放進哪一天要等帳單寄來才知道，那時交易早已確認；若只能作廢重記，就要為了一個只影響帳單的日子去動餘額與預算。**帳單的數字會跟著變**：帳單由交易算出來、不另存，所以一張已經結帳的帳單，在它的某筆交易被移到下一張（或從下一張移回來）之後，數字就不同了；那正是設入帳日要做的事。第 4 點要保護的是計入餘額、預算與往來的東西，它們沒有變，所以這不是第二個例外。規則見第 5 節的「信用卡帳單」。

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

### 預算與分配（migration `0007_fin_budgets.sql`）

M1c 第二個 PR 的資料庫變更全在這一個 migration；`0001`–`0006` 已合併，不改。預算屬於 `finance`（第 2 節），領域與專案的 id 是一般的 `uuid` 欄位，不加外鍵，寫入前由 use case 經 catalog 的 `VerifyReferences` 驗證存在且未封存（架構規範第 4 節第 5 點）。

- `fin_budget`：`id`、`scope`（`area`／`project`）、`area_id uuid`、`project_id uuid`、`effective_from date`、`amount numeric`、`created_at`。`scope` 決定要哪些欄位，多一個少一個都不行（`fin_budget_scope_columns_check`）：領域的預算有 `area_id` 與 `effective_from`，沒有 `project_id`；專案的預算有 `project_id`，沒有 `area_id` 與 `effective_from`。其他約束：`fin_budget_scope_check`（`scope` 的值）、`fin_budget_effective_from_check`（`effective_from` 是月的第一天）、`fin_budget_amount_check`（`amount >= 0`）、`UNIQUE (area_id, effective_from)`（`fin_budget_area_id_effective_from_key`）與 `UNIQUE (project_id)`（`fin_budget_project_id_key`）；NULL 與任何值都不相等，所以兩個唯一約束不會擋到另一種 scope 的列。
  - **領域的預算，一列是「從這個月起」**：某個月的預算是 `effective_from` 不晚於該月的列中最大的那一列，所以設一次就一直有效，直到後面的月份另設一列；`amount` 為 0 是「這個月起沒有預算」，用來結束先前的預算（`CONTEXT.md`：領域的預算每月一個額度，不累積）。專案的預算只有一列：整個專案一個總額。
  - 金額是本位幣，小數位數不超過它的 `decimals`；本位幣是 `fin_instrument` 的一列，CHECK 看不到它，所以由 domain 檢查。
- `fin_budget_allocation`：`budget_id`（外鍵，`ON DELETE CASCADE`，拿掉預算時分配一起刪）、`category_id`（外鍵到 `fin_category`，同一個 context 內）、`amount`；主鍵 `(budget_id, category_id)`，所以一個類別在一個預算裡只有一筆；`fin_budget_allocation_amount_check`（`amount > 0`）。分配是預算按類別切出的份額，類別本身沒有預算（`CONTEXT.md`：分配）。分配與預算之間的規則資料庫不檢查：分配的類別是支出方向、未封存、不是系統類別，一個類別與它的父類別不同時有分配，分配加總不超過預算。這些要同時看類別與預算，CHECK 看不到，由 domain 與 use case 檢查（第 5 節）。
- **Down** 還原成 `0006` 的 schema（有測試：與只套用到 `0006` 的資料庫比對 catalog 裡的欄位、約束、索引、trigger 與種子資料）。資料庫裡有預算時，Down **中止並報錯，不刪資料**，理由同 `0005`、`0006`：預算是使用者設定的資料，不該被 migration 靜默刪掉。訊息是 `cannot roll back 0007_fin_budgets: the database holds budgets`，HINT 說要先自行刪除預算（分配隨預算一起刪）。有測試：領域與專案的預算各一個案例，不動資料；帳本裡的交易不擋 Down。`0005`、`0006` 的測試改成先退到各自的版本再開始（`atVersion`）。

### 信用卡帳單（migration `0008_fin_card_statements.sql`）

M1d 的資料庫變更全在這一個 migration；`0001`–`0007` 已合併，不改。**帳單本身不存**：它是信用卡的交易在兩個結帳日之間加總出來的，讀的時候才算（第 5 節），所以不會與帳本不一致。存的只有帳戶的兩個日子，與交易上的入帳日。

- `fin_account`：加 `closing_day smallint`（結帳日）與 `payment_due_day smallint`（繳款日），都可為空。約束：`fin_account_closing_day_check` 與 `fin_account_payment_due_day_check`（各自 1 到 31）、`fin_account_statement_days_kind_check`（兩個日子只有 `kind = 'card'` 的帳戶才能有）、`fin_account_payment_due_day_closing_day_check`（有繳款日就要有結帳日）。卡可以兩個都沒有（沒有帳單）。日子大於當月的天數時以當月最後一天算，所以 31 一定是月底；那是用到日子的時候的事（`model.DateInMonth`），CHECK 只管 1 到 31。
- `fin_transaction`：加 `posted_on date`（入帳日），可為空。約束（為空時都通過）：`fin_transaction_posted_on_check`（`posted_on >= occurred_on`）與 `fin_transaction_posted_on_plan_check`（分期計畫的交易沒有入帳日：計畫的起點與每一期都有 `plan_id`，起點的 `plan_id` 就是它自己的 id，所以有 `posted_on` 就不能有 `plan_id`）。只有「有一筆信用卡的帳戶分錄」要看別的表（分錄的帳戶的 `kind`），CHECK 看不到，由 domain 與 use case 檢查（第 5 節）。沒有新索引：帳單的查詢從 `fin_posting` 的 `(account_id, instrument_id)` 索引進去，再以主鍵取交易。
- **Down** 還原成 `0007` 的 schema（有測試：與只套用到 `0007` 的資料庫比對 catalog 裡的欄位、約束、索引、trigger 與種子資料）。有任何帳戶有結帳日或繳款日、或有任何交易有入帳日時，Down **中止並報錯，不刪資料**，理由同 `0005`–`0007`：那是使用者給的資料，不該被 migration 靜默刪掉。訊息是 `cannot roll back 0008_fin_card_statements: the database holds closing days, payment due days or posting dates`，HINT 說要先自行清掉。有測試：只有結帳日、結帳日加繳款日、只有入帳日、以及拿掉「有繳款日就要有結帳日」的 CHECK 才放得進去的只有繳款日（守衛的第二個條件）各一個案例，都不動資料；沒有這些值的卡與帳本不擋 Down。`latestVersion` 改成 8；`0005`–`0007` 的測試用的 `atVersion` 從最新的版本往下退到各自的版本，所以不必改。

## 5. Use case（M1a、M1b、M1c、M1d）

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

交易邊界由 use case 決定：每個 Record*、確認、捨棄、作廢（含驗證所需的讀取與冪等鍵查詢）在同一個資料庫交易內完成，repo 不自行開交易，唯一的例外是 `TransactionRepo.List` 與 `TransactionRepo.Statement` 的唯讀快照。需要一個 `pkg/postgres` 的交易輔助（透過 context 傳遞 `pgx.Tx`）；use case 經 `usecase/port/out/tx.Transactor`（只有 `WithinTx`）使用它，由 `cmd/` 注入 `*postgres.DB`。`WithinTx` 不巢狀：`fn` 裡呼叫的任何東西都不能再開交易（`fn` 可以呼叫別的 use case，例如 `header` 經 catalog 的 `VerifyReferences` 驗證參照，只要它們自己不開交易）；傳給 `WithinTx` 的 context 已帶著交易時，它回傳 `ErrNestedTx`，不執行 `fn`、不送任何語句，開著的交易照舊，呼叫它的 `fn` 把錯誤回傳，整個交易回滾。`TransactionRepo.Insert` 以多條語句寫入交易、分錄與標籤，必須在資料庫交易內呼叫，否則平衡檢查會在第一條語句提交時拒絕。

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
- `ConfirmTransactions`、`DiscardDraft`、`VoidTransaction` 以計畫為單位：給起點的 id，帶著整個計畫。期也是各自的交易，`ListTransactions` 會列出它們（只有給 `ExcludePlanPeriods` 才只列起點，見「網頁用的讀取」），所以「確認列出的全部草稿」會把起點與各期的 id 一起送來：**起點也在請求裡的期，由起點的展開涵蓋，不算錯，也不重複處理**（每一筆只確認一次，整個呼叫仍是一個資料庫交易）；起點不在請求裡的期回 `ErrPartOfPlan`（錯誤列出所有這樣的期），而且不鎖那一期，即使請求裡有別的計畫的起點。`ConfirmTransactions` 的輸出是請求的每個 id 各一筆（重複的 id 算一次），依請求的順序，是確認之後的樣子：給了的期在輸出裡，沒給的期不在（只給起點就只有起點一筆，各期用 `GetInstalmentPlan` 讀）。`DiscardDraft` 與 `VoidTransaction` 只收一個 id，起點不可能與期同在一個請求裡，所以期的 id 一律是 `ErrPartOfPlan`。捨棄先刪各期、再刪起點（自我外鍵）；作廢給起點，各期已經到期也可以，作廢後整個計畫從餘額與計畫列表消失。
- **鎖的順序**：先不鎖地讀一次，把請求的 id 分類（一般交易、起點、期）；然後所有呼叫（確認、捨棄、作廢、改期）都照同一個順序鎖：先是不是期的那些，也就是起點與一般交易，依 id 升冪 `SELECT ... FOR UPDATE`；再來是每個已鎖住的起點的計畫，它的各期依 id 升冪（起點也在請求裡的期併入這個展開，不另鎖）。一個計畫的各期只有握著它起點的那個呼叫會鎖，所以各期的鎖永遠不必等，也不會成為環的一部分；兩個有重疊的呼叫在起點與一般交易上依同一個 id 順序鎖，所以不會死結；後到的在鎖上等，等到時狀態已變，回 `ErrNotDraft`／`ErrNotConfirmed`。各期要等起點鎖到手才查：改期會把各期換成新的，起點鎖到之前讀到的各期，鎖到時可能已經不在。分類到鎖之間計畫被刪掉了，也回錯誤而不是靜默略過。use case 的測試以假的 repo 紀錄鎖的順序（起點與一般交易升冪，然後各計畫的各期升冪；被拒絕的期沒有鎖；每一筆只鎖一次）與分類後才不見的期；對真實資料庫的併發測試見第 6 節。
  - **由起點涵蓋的期，鎖住起點之後要再對一次。** 分類時它是請求裡的一期、由起點涵蓋，不另鎖；但改期可以在分類之後、鎖之前提交，把那一期換成新的，鎖住起點之後計畫已經沒有這一期。所以鎖住每個起點、讀到它現在的各期之後，請求裡由它涵蓋的期都必須在其中；不在的，與不存在的 id 一樣回 `ErrNotFound`（錯誤指名那些 id），整個呼叫什麼都不確認，不會把它當成已涵蓋、輸出一筆空的交易。`ConfirmTransactions` 的輸出迴圈也不靜默略過：請求的 id 沒有確認的結果時回 `ErrNotFound`，不補一筆空的。測試：use case 層以假的 repo 的 `beforeLock` 在起點被鎖時先做一次改期；真的 PostgreSQL 上改期握著起點的鎖、確認在鎖上等（第 6 節）。
- `GetInstalmentPlan(planID)` → `InstalmentPlan{Origin, Periods}`（各期依位置）；它讀的是存起來的交易，沒有依日期算的金額，所以不看今天；id 不是起點（一般交易、期、不存在）回 `ErrNotFound`。`ListInstalmentPlans{AccountID?, IncludeFinished}` → `InstalmentPlanSummary{PlanID, AccountID, InstrumentID, Principal, Charged, Remaining, NextDueOn}`：只含已確認、起點日期在今天或之前的計畫，以今天為準。計畫從起點的日期起算，與 `GetBalances` 一樣（起點的 `instalment` 分錄那天才進餘額）：起點日期在今天之後的，兩邊都沒有。三個金額都是**大小**，不為負：`Charged` = 已到期各期的本金（不含利息），`Remaining` = `Principal` − `Charged`，也就是這個計畫還沒入帳的本金。`Remaining` 與 `Balance.UnbilledInstalment` 是同一個量、符號相反：後者是 `instalment` 分錄的加總，為 0 或負（−11,000 表示還有 11,000 要在之後的各期入帳），一個帳戶的 `UnbilledInstalment` 等於它所有計畫的 `Remaining` 之和取負（有測試逐日比對）。`NextDueOn` 為空表示已全部到期，預設不列已全部到期的（`IncludeFinished` 才列）；起點日由新到舊。以一條彙總查詢算出，所以是同一份快照。
- `RescheduleInstalment{PlanID, Schedule, ChangePast}` → `InstalmentPlan`：把計畫的排程整個換成新的。`Schedule` 與記錄時是同一個 `InstalmentSchedule`，檢查也一樣（日期遞增、第一期不早於起點、本金為正、利息與利息類別、本位幣金額），所以各期的扣款日、本金、利息、利息類別與**期數**都可以變。不變的有兩樣：起點從頭到尾不動；新排程的本金加總必須等於計畫的本金（起點的 `instalment` 分錄），否則回 `*PrincipalMismatchError`（`errors.Is` 為 `ErrPrincipalMismatch`，帶著兩個金額：排程加起來的與計畫的）。總額變了就是購買變了，要作廢重記；提前清償就是改期：到期的各期原樣再送，把剩下的本金放進清償那天的一期。草稿與已確認的計畫都可以，已作廢的回 `ErrPlanVoid`；id 不是起點（一般交易、期、不存在）回 `ErrNotFound`。已經到期的期要改（機構實際扣的未必是排的），得帶 `ChangePast`，見下。
  - **這是「已確認的交易不能修改」的唯一例外**（不變條件 4）：被改的是排程，也就是機構何時、扣多少，那會變；購買（起點）與總額不變。做法是刪掉舊的各期、寫入新的各期，所以**各期的交易 id 會變**（鍵 `instalment:<起點 id>:<位置>` 不變，位置 1…N），舊的各期不再存在。新的各期用記錄時的同一份程式碼做（`InstalmentSchedule.periods`）：沿用起點的說明、商家、專案、領域與標籤。新的各期取計畫的狀態：草稿計畫的是草稿，已確認計畫的是已確認，`confirmed_at` 是改期當時的資料庫時間。寫入仍是「先寫成草稿、再確認」：`Insert` 只收草稿，沒有放寬，確認走 `ConfirmTransactions` 同一條路（`SetStatus`），資料表的狀態與時間 CHECK 照常把關，平衡檢查在提交時檢查每一筆新的期。
  - **排程是整個換掉的**：新排程就是計畫從此的全部各期，已經到期的也在裡面。所以提前清償要把到期的各期原樣再送一次（日期、本金、利息都照舊），再加清償那天的一期放剩下的本金；沒有「只送改的那幾期」這種用法。
  - **過去是固定的，除非帶 `ChangePast`（`RescheduleInstalmentInput` 的欄位，預設 false）。** 過去的期是計畫現在的各期中扣款日嚴格早於今天的；今天以時鐘為準，今天到期的不算過去，可以改。沒有 `ChangePast` 時，新排程裡扣款日早於今天的各期，必須正好是這些期：同一天、同樣的本金、利息與利息的本位幣金額，順序也一樣；過去的期只要有一期有利息，利息類別也不能換（沒有一期有利息時，類別不是過去的一部分，可以換）。過去的期不能少、不能多、不能被改，否則回 `*PastPeriodsChangedError`（`errors.Is` 為 `ErrPastPeriodsChanged`；`Differences` 逐期列出位置、扣款日，以及是少了、多了還是哪些欄位不同，`InterestCategoryChanged` 說類別換了；訊息說明要 `ChangePast`），什麼都不寫。草稿的計畫一樣。這一關在排程本身的檢查之後、寫入之前，用的是鎖住之後讀到的各期。
  - **`ChangePast` 的範圍。** 帶著它，過去的期可以隨新排程改，也就是已確認的交易被改了，後果是：已到期各期的利息支出與它的本位幣金額會變（不變條件 3 的「寫入後不再變動」因此有這個例外）；過去的帳戶餘額與未到期分期會變；某個過去日子的、已確認的對帳可能因此過時，而且不會被偵測（與上面對帳的已知限制同一類）。不帶它，過去固定，這些都不會發生。
  - **利息類別**：新排程的利息類別與記錄時一樣要能用（不是已封存、是支出類別、不是系統類別）。唯一的放寬是計畫的各期現在用的那一個：它在記錄之後被封存了，仍可以沿用，否則用了已封存類別的計畫連改期都不行；換成別的已封存的類別仍回 `ErrArchived`。「現在用的」以計畫現在的各期為準：換成別的類別之後，原來那個已封存的就不再放行；沒有任何一期有利息的計畫沒有「現在用的」，已封存的類別就是已封存。
  - 一個 `WithinTx`：先鎖計畫（上面的鎖的順序），鎖住之後重讀計畫，狀態與有沒有被刪都在鎖下檢查，由 domain（`InstalmentPlan.Reschedule`；今天與 `ChangePast` 由 use case 傳進去，domain 不讀時鐘）檢查排程與過去的各期、做出新的各期，通過了才寫（刪、寫、確認）；被拒絕的什麼都不寫。沒有冪等鍵：同樣的請求做兩次得到同樣的排程，第二次把各期重寫一次，沒關係。
  - 餘額、未到期分期、計畫列表與對帳都是依日期算的，所以改期之後各期從新的日子起算：一期從未來移到今天或之前，帳戶餘額多了它的本金與利息，未到期分期少了本金，反過來也一樣；沒有利息的期移動時，兩者加起來不變。移到今天不是過去，移到今天之前，或把今天之前的期移走，才要 `ChangePast`。草稿的期移到對帳那天或之前，那天的對帳就被它擋下（`ErrPendingDrafts`），移回去就不擋。這些都不特別處理，也沒有快取。
- `RecordTrade{Direction, AccountID, InstrumentID, Quantity, CashAccountID, CurrencyID, Amount, Fees}`：`Direction` 是 `buy` 或 `sell`；`AccountID` 持有被買賣的標的，`CashAccountID` 收付金額。
- **已知的限制**：被分期的總額不能改（改了就是改了那筆購買：作廢整個計畫、重記）；提前清償就是改期，把到期的各期原樣再送一次、剩下的本金放進清償那天的一期（`RescheduleInstalment`）。各期是一般的交易，會出現在 `ListTransactions`（可用 `PlanID` 篩選整個計畫；給 `ExcludePlanPeriods` 則計畫只列起點，它代表整個計畫）；不自動確認各期。

### 預算與分配（M1c，`app/finance`）

預算只用來比對與告警：不從現有的錢裡預留，超過了什麼都不擋，只回報（`Over`）。`usecase/budget` 一個套件實作七個 use case，其中五個是預算本身，port 在 `usecase/port/in/usecase/budget.go`；另外兩個是網頁用的讀取（`GetSpending`、`GetSpendingTrend`），見本節最後一段。`BudgetRepo`（`usecase/port/out/repo/budget_repo.go`）存取預算與分配，也讀「花了多少」（`Spending`，只有一條 SQL，見下）；預算的規則在 `domain/model/budget.go`，「花了多少」讀成領域、專案、月份、類別的各種樣子在 `domain/model/spending.go`。

**寫入**（每個都在一個資料庫交易內，驗證失敗什麼都不寫）

- `SetAreaBudget{AreaID, Month, Amount, Allocations []Allocation{CategoryID, Amount}}` → `AreaBudget{AreaID, From, Amount, Allocations}`：設定領域**從該月起**的預算。`Month` 必填（`ErrMonthRequired`），取它所在的月（以它自己的時區讀，與 `model.DateOf` 相同），存成該月第一天，所以月內任何一天都行。同一個領域、同一個月再設，取代那一列的金額與全部分配，列的 id 與 `created_at` 不變；其他月份的列不動，也不看晚於該月的列。`Amount` 為 0 是從該月起沒有預算。結束預算（`Amount` 為 0 且沒有分配）對已封存的領域也放行，只要求領域存在，因為已封存的領域仍照列它的預算、預算在被後面的月份取代前一直在效力中，不能結束就永遠留著，而結束不是新的參照；其他的預算（金額大於 0、為負、或有分配）對已封存的領域仍回 `*ReferenceError`。
- `SetProjectBudget{ProjectID, Amount, Allocations}` → `ProjectBudget`：整個專案一個總額，再設就取代金額與全部分配。專案沒有「0 就是沒有預算」：0 是金額為 0 的預算，花任何錢都算超過；沒有預算用 `RemoveProjectBudget{ProjectID}`（連分配一起刪）。專案沒有預算時，拿掉它回 `ErrNotFound`（寧可讓打錯 id 的呼叫看得到，不靜默成功）；專案已封存也可以拿掉，拿掉預算不是新的參照，所以不問 catalog。
- 驗證：領域或專案經 catalog 的 `VerifyReferences` 必須存在且未封存（`*ReferenceError`；領域結束預算的例外見上）。分配的類別必須存在（`ErrNotFound`，錯誤指出是第幾筆）、未封存（`ErrArchived`）、是支出方向（`ErrWrongCategoryDirection`）、不是系統類別（`ErrSystemCategory`）；一個類別只能一筆（`ErrDuplicateAllocation`）；一個類別與它的父類別不能同時有分配，不論誰先（`ErrParentAndChildAllocated`）；分配加總不超過預算，剛好等於可以（`ErrAllocationsExceedBudget`，訊息帶兩個金額）。預算不為負（`ErrBudgetAmountNegative`），分配大於 0（`ErrAmountNotPositive`），兩者的小數位數都不超過本位幣（`ErrTooManyDecimals`）。
- `BudgetRepo.SaveAreaBudget`／`SaveProjectBudget` 以三條語句寫（`INSERT ... ON CONFLICT ... DO UPDATE` 存那一列、刪舊分配、插新分配），所以必須在資料庫交易內呼叫。每個儲存都從那條 upsert 開始，它鎖住那一列到交易結束，所以同一個月的兩個同時進來的設定一個接一個，後到的整個取代先到的，不會留下一邊的金額加另一邊的分配（有真的 PostgreSQL 的併發測試：同時存同一個月，剩下的是其中一個呼叫設的、完整的那一份）。

**「花了多少」** 是已確認交易的 `expense` 分錄的 `base_amount` 加總，`occurred_on` 不晚於今天（注入的時鐘，與餘額同一個「今天」：日期在未來的支出到那天才算）；屬於哪個月由 `occurred_on` 決定。**定義它的 SQL 只有一條**，`BudgetRepo.Spending`（sqlc 的 `ListSpending`）：領域、專案、月份與類別的讀取都是 domain 的純函式由它的列算出來的，沒有第二處在數「什麼算花了」。

- **任何種類的交易都算**：手續費是別種交易（轉帳、收入…）裡的 `expense` 分錄，分期的利息是各期（種類 `instalment`）裡的 `expense` 分錄，都在被收取的那一天、那個月算；消費分期的起點在它的日子全額算，每期的本金不是支出；帳單分期的起點沒有 `expense` 分錄，不算；對帳的「未記錄」是 `expense` 分錄，照算，落入「未分配」。分攤只算自己的份（`expense` 分錄就是自己的份，替別人付的是 `dealing` 分錄）；折扣不減（`base_amount` 是實付）；回饋（`reward` 分錄）與收入不減。外幣支出用交易當天固定下來的 `base_amount`。草稿與作廢的交易哪裡都不算。
- **歸哪個領域、專案**：交易自己的 `area_id`、`project_id`。沒有領域的支出不屬於任何領域，不在領域的任何數字裡；分期各期沿用起點的領域與專案。屬於專案的支出只計入專案預算，但仍計入領域的統計（`SpentWithProjects`）。
- **一條 SQL，列由 domain 讀。** `BudgetRepo.Spending(SpendingFilter{From?, To, ProjectID?, InProject?})` 是一條語句：已確認、`book = 'expense'`、`occurred_on` 在 `From` 與 `To` 之間（兩端都含；`From` 可省略，省略就是沒有下限）、`ProjectID` 有給時只要那個專案的交易、`InProject` 為真時只要屬於某個專案的交易（給了 `ProjectID` 就已經是）；依月份（`occurred_on` 所在月的第一天，從 timestamp 切出來，不經時區）、領域、專案、類別與它的父類別分組，加總 `base_amount`。沒有領域或沒有專案的交易也有列，那一欄是空的（NULL），所以一個月的列加起來就是這個月全部花的（`InProject` 為真時沒有專案的列不在其中）；沒有 `ORDER BY`，列沒有特定的順序。各處要的樣子由 `domain/model` 的純函式從這些列算出（各有單元測試）：`AreaSpendingOfMonth`（依領域、類別、是否屬於專案；各專案的併成屬於專案的那一列；沒有領域的列與別的月份的列不算）、`ProjectSpendingOf`（依專案、類別，不分月與領域；沒有專案的列不算）、`TopLevelSpendingOfMonth`（一個月的頂層類別，見「網頁用的讀取」）與 `TotalsByMonth`（每個月的總額）。`ProjectID` 這個篩選只為了讀單一專案時，不必把每個專案的列都讀進來；`InProject` 是為了專案的讀取不必把整個帳本沒有專案的支出（絕大多數）都讀進來再丟掉：`GetProjectBudgets` 一律設它，領域、月份、類別與總額的讀取都不設。
- **落入哪個分配**：自己的類別的分配，否則父類別的分配，否則「未分配」（`model.NewBudgetStatus`，純函式）。每一列帶著類別的父類別 id（`Spending.ParentID`，SQL 讀 `fin_category.parent_id`），歸入哪個分配由 domain 決定，規則只有一份，也就不必在 SQL 裡重做一次「有效的預算是哪一列」。沒有預算的領域或專案沒有分配，所以花的全部是「未分配」，`Over` 為假。分配自己也有 `Over`（`AllocationStatus.Over`，同樣由 `NewBudgetStatus` 設定）：落入它的 `Spent` **大於** `Amount` 才算，剛好花完不算；子類別的支出落入父類別的分配，所以會讓它超過；沒有東西落入的分配不超過。分配的 `Over` 與預算的 `Over` 各自成立（分配超過了，預算不一定超過，反過來也一樣），什麼都不擋；頁面直接用這個欄位，不重算。
- **讀多遠**：領域的範圍是「該月第一天到該月最後一天與今天中較早的一天」（`From` 與 `To` 都給），還沒開始的月份不問 repo；專案是「到今天」，不設下限（`From` 省略），不分月，只要屬於專案的支出（`InProject`），給了 `ProjectID` 時只讀那個專案。

**讀取**

- `GetAreaBudgets{Month}` → `GetAreaBudgetsOutput{Month, Areas []AreaBudgetStatus{AreaID, Budget?, Spent, SpentWithProjects, Allocations []AllocationStatus{CategoryID, Amount, Spent, Over}, Unallocated, Over}}`：`Month` 必填，同樣取它所在的月。列出該月有預算在效力中、或有已確認支出的領域，依 catalog 列出的順序（名稱），已封存的也列（它花的仍然算）；兩者都沒有的領域不列。該月的預算在 domain 由全部的預算列挑出（`model.BudgetsInForce`：`effective_from` 不晚於該月的列中最大的那一列，晚於該月的列不適用，金額為 0 的列在效力中就是沒有預算），所以 `BudgetRepo.AreaBudgets` 回傳所有領域的所有列，不依月份篩；每列連同它的分配，依類別名稱再依 id 排序（名稱只在同一個父類別底下唯一），一條語句讀，所以預算與它的分配一致。`Spent` 是不屬於專案的支出，預算拿它比對；`SpentWithProjects` 是該領域該月全部的支出（統計用的數字）；分配的 `Spent` 與 `Unallocated` 也只算不屬於專案的，所以 `Spent` 等於各分配的 `Spent` 加 `Unallocated`；`Over` 是有預算且 `Spent` **大於**預算，剛好花完不算；分配的 `Over` 是落入它的 `Spent` **大於**它的 `Amount`，與預算的 `Over` 各自成立。
- `GetProjectBudgets{ProjectID?}` → `GetProjectBudgetsOutput{Projects []ProjectBudgetStatus{ProjectID, Budget?, Spent, Allocations, Unallocated, Over}}`：有預算或有已確認支出的專案，依 catalog 的順序（含已封存的），`Spent` 是它全部的支出（到今天為止，不分月）；分配的 `Over` 同上。給了 `ProjectID` 時只問那一個專案，不列 catalog 的全部專案：先以 `VerifyReferences` 確認它存在（找不到回 `ErrNotFound`，不讀 repo；已封存的放行：它花的仍然算），與 `GetDealings(partyID)` 同一個做法，`Spending` 也只讀它的列（`SpendingFilter.ProjectID`）；這個專案沒有預算也沒有支出時是空的結果，不是錯誤。
- 預算或支出指著 catalog 列表裡沒有的領域或專案時回 `ErrNotFound` 並指名，不猜；沒有東西可排序時不呼叫 catalog。
- 讀取不開資料庫交易，也不用快照：預算與分配是一條語句，花了多少是另一條，同時有人改預算時，這一次的數字可能一邊新一邊舊，下一次就對了。
- 這幾個 use case 還沒有 MCP 工具（M3）。

### 信用卡帳單（M1d，`app/finance`）

**帳單**（Statement）是信用卡在兩個結帳日之間記了什麼、期末欠多少。它由交易算出來，讀的時候才算，不存：所以不會與帳本不一致，也沒有「重新產生帳單」這回事。`account` 的 `Service` 多一個 `SetAccountStatementDays`；`ledger` 的 `Service` 多 `SetPostingDate`、`GetCardStatement`、`ListCardStatements`，`RecordExpense` 多一個選填的 `PostedOn`。日子的算術在 `domain/model/statement.go`，入帳日的規則在 `domain/model/posted_on.go`，帳單的讀取在 `TransactionRepo.StatementFigures` 與 `Statement`，都沒有 MCP 工具（M3）。

**帳戶的結帳日與繳款日**

- `CreateAccountInput` 多 `ClosingDay`、`PaymentDueDay`（指標，可省略）；`SetAccountStatementDays{ID, ClosingDay, PaymentDueDay}` 改或清掉它們：兩個日子**一起取代**，nil 就是清掉它代表的那一個（只給結帳日，繳款日就被清掉）；沒有結帳日卻給繳款日是錯的，所以清掉結帳日要連繳款日一起清。輸出的 `Account` 帶這兩個欄位。
- 驗證在 `model.Account`（`NewAccount`、`WithStatementDays`）：只有 `card` 有（`ErrNotACard`）；1 到 31（`ErrDayOutOfRange`，訊息說是哪一個）；有繳款日就要有結帳日（`ErrDueDayWithoutClosingDay`）。兩個都沒有的帳戶，清掉不算錯（沒有東西要拒絕）。已封存的卡也可以改日子：它的交易還在，帳單仍會被讀。資料庫的 CHECK 是最後一道。
- **結帳日不留歷史。**帳單是用卡現在的結帳日切的：改了結帳日，過去的帳單就照新的日子重切；本檔不存「哪一天起改成幾號」。這是刻意的：要留歷史就得存一張日子的版本表，而這裡要的是照銀行現在寄來的帳單核對。有測試（`cmd/`：結帳日從 5 號改成 10 號，同一天落在另一張帳單，過去的帳單跟著變）。

**帳單怎麼切**（`StatementContaining`、`StatementsClosingBetween`、`StatementPeriod.DueDate`）

- 每個月的**結帳日期**是該月的第 `closing_day` 天；該月沒有那一天時是該月最後一天（31 一定是月底，二月的 29、30、31 在平年是 28、閏年是 29：`DateInMonth`）。一張帳單涵蓋「上一個結帳日期的隔天」到「它自己的結帳日期」，兩端都含，所以每一天剛好在一張帳單上：結帳日當天在那張帳單，隔天是下一張。
- 帳單的**繳款日期**是結帳日期之後、嚴格晚於它的第一個「日是繳款日」的日期，月份較短時同樣取月底：繳款日大於結帳日就是同一個月，小於或等於就是下個月，跨年也一樣。與結帳日同一天算下個月：結帳那天還不知道要繳多少。月底的邊界照同一條規則：二月結在 28 號、繳款日是 29，二月的「29」就是 28，不嚴格晚於結帳日，所以在三月 29 號（閏年二月有 29，就在二月 29）。
- `Closed` 是結帳日期**早於今天**：結帳日當天帳單還沒關，隔天才關。「今天」是注入的時鐘（與 `GetBalances` 同一個，時區也是）。

**什麼算進帳單**

- **帳單日期**（statement date）是入帳日，沒有就是交易自己的日期（`Transaction.StatementDate`）。交易在涵蓋它的帳單日期的那張帳單上。
- 只算已確認交易在**該帳戶的 `account` 分錄**，而且帳單日期不晚於今天與該帳單的結帳日期中較早的那一天：日期在未來的交易到那天才算，與餘額同一條規則。草稿與作廢的交易哪張帳單都不算。分期消費的起點沒有帳戶分錄（只有 `instalment` 分錄），所以不在任何帳單上；各期有帳戶分錄，在扣款日所在的那張帳單上（扣款日是結帳日期就在那一張）；未到期分期不是帳單的一部分。帳單分期的起點有帳戶分錄（把已出的帳單還清），是那一天的一筆貸方。
- 每個標的各有一組數字（一張卡可以有好幾個標的）：`Previous`（帳單日期早於這張帳單的分錄加總）、`Charges`（這張帳單裡為負的分錄加總，以正數表示）、`Credits`（為正的分錄加總：繳款、退款、回饋）、`Closing = Previous − Charges + Credits`。`Previous` 與 `Closing` 與餘額同號，負的是欠款。分錄是一筆一筆算的：同一筆交易在卡上的兩筆分錄（轉帳與它的手續費）各歸各的正負。**這張帳單沒有任何分錄、期初也是 0 的標的不列**：沒有話可說（與 `GetBalances` 不同，那邊分錄加總為 0 的標的仍列出，因為餘額為 0 本身就是要看的事）。
- 一張帳單的 `Closing` 就是下一張的 `Previous`：domain 層是兩張帳單的天數相接（以 1 到 31 的每個結帳日、四年的每一天逐日驗證），repo 層是數字相等（用另一種加法，直接從存下來的交易加總，驗證每一張）。

**入帳日（`posted_on`）**

- `SetPostingDate{TransactionID, PostedOn}`：設定、更改或清除（nil），回傳交易；給它已經有的值再呼叫一次也成功，什麼都不變。草稿與已確認的交易都可以，作廢的不行（`ErrTransactionVoid`；作廢的交易保留它最後的入帳日）。只有在 `kind` 為 `card` 的帳戶上有 `account` 分錄的交易可以有入帳日，否則 `ErrNoCardPosting`（付款的是對象、或只有別的帳戶的交易都是）；分期計畫的起點與各期都不行，`ErrPartOfPlan`：各期的日期本來就是機構扣款的那一天，改期又會把各期整批換掉，而消費分期的起點沒有帳戶分錄、帳單分期的起點是還帳單的那一筆，不是機構放進帳單的日子；資料庫的 `fin_transaction_posted_on_plan_check` 是最後一道。入帳日早於交易日期回 `ErrPostedBeforeOccurred`（`Validate` 與資料庫的 CHECK 各擋一次；同一天可以）。**清除也先問這些：**不能有入帳日的交易，連「清掉」也回同一個錯，因為它不是該被問入帳日的交易。
- `RecordExpenseInput.PostedOn`：記支出時就帶入帳日（只有支出；其他的 `Record*` 沒有）。規則同上，所以從現金付的支出帶入帳日是 `ErrNoCardPosting`，帶 `Instalments` 是 `ErrPartOfPlan`。鍵已用過時照其他的 `Record*`：回傳當初記下的交易，不看這一次的內容。
- `Transaction`（`GetTransaction`、`ListTransactions`、確認與作廢的輸出）帶 `PostedOn`。
- **入帳日只決定交易在哪一張帳單上**（不變條件 8）：餘額、預算、往來、`ListTransactions` 的日期篩選與對帳都照舊只看 `occurred_on`。測試都把這件事寫成斷言：有入帳日之後，結帳日的餘額與各月的預算不變。

**讀帳單**

- `GetCardStatement{AccountID, ClosingOn?}` → `CardStatement{From, ClosingOn, DueOn?, Closed, Figures, Transactions, Drafts, Charges}`：輸入的 `ClosingOn` 是要看的那張帳單裡的任何一天（通常就是它的結帳日期），不給就是今天，也就是現在開著的那張。`Transactions` 是帳單上的交易，每筆是 `GetTransaction` 回傳的樣子（含分錄的類別與備註），加上帳單日期與它在這張卡上動了多少（每個標的、每筆帳戶分錄一項）：**由舊到新**，依帳單日期，再依交易日期，同一天先記的在前，也就是銀行帳單一行一行排下來的順序，因為這個功能是要拿它與銀行寄來的帳單逐行核對（與 `ListTransactions` 相反，那個是新的在前）。沒有分頁：一張帳單的交易是有限的。`Drafts` 是在這張卡上有帳戶分錄、帳單日期落在這張帳單裡的**草稿**張數：不在 `Figures`，也不在 `Transactions`，是還等著確認、確認後會改變數字的張數；日期在今天之後的草稿也算，因為它到那天就在這張帳單上（與數字不同，這個數不受「今天」限制）。
- **`Charges`：這一期的消費花在哪裡。** `GetCardStatement` 另外回 `Charges`：每個有扣款的標的一筆 `StatementCharges{InstrumentID, Categories, Shares, Other}`，順序與 `Figures` 相同；三者加起來等於那個標的的 `Figures.Charges`，一分不多、一分不少（有測試：`model.ChargesOfCard` 的單元測試、`GetCardStatement` 的 use case 測試，與 `cmd/` 以真的 use case 與 PostgreSQL 把帳單與 `GetSpending` 對起來的測試，後者含用卡借給對方的往來、與銀行付手續費的轉帳）。算法是 `model.ChargesOfCard`：一筆交易在這張卡上的扣款（卡的 `account` 分錄中為負的，加起來），依它在這個標的的各行、照分錄的順序分出去，每一行最多取到扣款還剩的部分；只取**這張卡付的**行。`expense` 簿的行給它的類別的**頂層類別**，與 `GetSpending` 同一個定義，所以同一筆 50 的利息，在帳單上是「利息」，在支出頁也是「利息」。哪些行是卡付的，要看種類：`expense`、分期各期與對帳自己有支出，支出行是這筆交易的付款帳戶付的，卡被扣了就是卡付的（分期各期的利息）；其他種類（轉帳、往來、收入、回饋、換匯、買賣、帳單分期）的 `expense` 行都是手續費，手續費是付它的那一筆 `account` 分錄付的，只有那一筆是這張卡的分錄，才算這張卡的。哪一筆分錄付哪一筆手續費，與 `Validate` 認手續費是同一個配對（`Transaction.feePairs`：同標的、同金額，建構時付款的 `account` 分錄在前、手續費在後，每一筆手續費配它前面那一筆，金額相同時也各配各的），沒有第二套規則。所以從卡轉到銀行，卡付 10、銀行付 15，帳單上的手續費是 10，不是 25：銀行付的 15 是花掉了（`GetSpending` 有），卻不是這張卡被扣的。讓對方欠我變多的 `dealing` 行（我替別人付的）只有在 `expense` 種類裡給 `Shares`，那是分攤；種類是 `dealing` 的交易（我用這張卡借錢給對方、替對方付款）是往來，不是分攤，給 `Other`；剩下的（轉出卡的轉帳、往來、分期各期的本金）也給 `Other`。一筆交易用這張卡和別的帳戶一起付，卡只被扣它那一份，所以不會多分。哪個類別是頂層，由 use case 讀一次類別清單（含已封存的）查出；帳單上沒有交易就不讀。網頁只寫這些行，不自己加總，也不自己分。
- `ListCardStatements{AccountID, From, To}` → 結帳日期在 `[From, To]`（兩端都含）的帳單，新的在前，只有數字、沒有交易；範圍內沒有結帳日期就是空的清單。兩端都要給（`ErrDateRangeRequired`），`To` 不能早於 `From`（`ErrDateRangeReversed`），範圍最長十年（`ErrStatementRangeTooWide`，`usecase.MaxStatementRangeYears`）：不設上限，一個寫得出來的日期範圍就能要上萬年的帳單。晚於今天的結帳日期照列（還沒有交易，只有帶進來的 `Previous`）。
- 不是信用卡、或卡沒有結帳日：`ErrNoStatements`；帳戶不存在：`ErrNotFound`。兩者都在讀 repo 之前就回。

**一致性與鎖**

- 同一個回答裡的數字必須相容。`TransactionRepo.StatementFigures`（幾張帳單的數字）只有一條 SQL 語句，所以 `Previous`、各張的進出與 `Closing` 來自同一份資料，也就是一張的 `Closing` 是下一張的 `Previous`。`TransactionRepo.Statement`（一張帳單的數字、交易與草稿張數）用 `DB.WithinSnapshot`（`REPEATABLE READ` 的唯讀交易，與 `List` 同一個做法，架構規範第 6 節的例外），所以列出的交易就是數字加起來的那些：別的連線在讀的中途確認了一筆，這一次的回答裡沒有它（有測試：以 pgx tracer 把變更放在兩條語句之間）。
- `SetPostingDate` 在一個資料庫交易內：先不鎖地讀一次，不能有入帳日的（計畫的起點與各期、作廢的）直接回錯，**不鎖**，與 `lockActed` 對「起點不在請求裡的期」同一個做法：各期的鎖只有握著起點的呼叫會拿，這個呼叫不拿，所以各期的鎖仍然永遠不必等。其餘的 `SELECT ... FOR UPDATE` 鎖住那一列（`GetForUpdate`），鎖住之後才檢查狀態與規則（等鎖的時候它可能已被作廢或捨棄）、才寫。它只拿一把鎖、第一個拿、拿的就是確認、作廢與捨棄對一般交易拿的那一把，所以在上面鎖的順序裡只是「一般交易」那一段的一筆，不會與它們互相等待，也不會是環的一部分。結果一定是某一個先後順序：作廢先、日期後，日期回 `ErrTransactionVoid`；日期先、作廢後，作廢的交易帶著那個日期；確認與日期不論誰先，都是已確認、有日期的交易；捨棄先，日期回 `ErrNotFound`。

**兩個已知的後果**

- 結帳日不留歷史（見上）：改結帳日會把過去的帳單重切。
- **對帳不變。**`Reconcile` 仍然以交易日期比對餘額。入帳日與交易日期在結帳日前後不同時（例如結帳日當天的消費，銀行放進下一張帳單），要與銀行寄來的帳單核對，比的是那張帳單的 `Closing`，不是 `GetBalances` 在結帳日的餘額：後者有那筆消費，前者沒有。用結帳日的餘額去對銀行的帳單，會對出一筆不存在的差額。`cmd/` 的測試把這個後果寫成斷言（結帳日當天的消費被移到下一張帳單後，對帳那天的記錄餘額仍是含它的，差額是它的金額），所以日後有人改了 `Reconcile` 會知道是在改這個行為。

### 網頁用的讀取（`GetSpending`、`GetSpendingTrend`、`GetDraftSummary`、`GetPreviewBalances`、`ListTransactions` 的篩選）

唯讀的網頁（Cloudflare Access 之後、伺服器端渲染）要四個既有讀取給不了的數字，和 `ListTransactions` 的幾個篩選。它們都在 port 後面，沒有新的資料表、沒有 migration，也還沒有 MCP 工具（M3）。

**花了多少，只有一個定義。** `GetSpending{Month}` 回傳該月各頂層類別花了多少（`TopLevel`：每個頂層類別有 `Total`（它自己與底下全部子類別）、`Own`（只花在它自己身上的）與 `Children`（花了錢的子類別，金額大的在前、同額依類別 id）；`TopLevel` 依 `Total` 大的在前、同額依類別 id；輸出的 `Total` 是各頂層類別的 `Total` 加總），`GetSpendingTrend{From, To}` 回傳區間內每個月各一個總額（最舊的在前；沒花的月份與還沒開始的月份也在，總額為 0）。「花了多少」仍是「預算與分配」那一段的定義：已確認交易的 `expense` 分錄的 `base_amount`，`occurred_on` 不晚於今天，任何種類的交易都算（手續費、分期的利息、「未記錄」照算），折扣與回饋、收入都不減，分攤只算自己的份，草稿與作廢的不算。這兩個讀取只是不依領域或專案分組，也不把沒有領域的支出排除：

- **頂層類別，子類別一定在父類別底下。** 子類別的支出歸在它的父類別之下，不論父類別自己有沒有花錢：只花在子類別時，父類別的 `Own` 為 0，`Total` 是子類別的加總；沒花錢的類別（它自己與底下都沒有）不在其中，已封存的類別照列（它花的仍然算）。這是 `model.TopLevelSpendingOfMonth`（純函式，由一個月各（領域、專案、類別）的列算出），頁面不必拿父類別 id 把子類別加回去：頁面只格式化（架構規範第 11 節）。
- **SQL 只有一條。** 定義「花了多少」的只有 `BudgetRepo.Spending`（sqlc 的 `ListSpending`）：已確認、`book = 'expense'`、`occurred_on` 在兩天之間（兩端都含，下限可省略）、可選的專案 id、可選的「只要屬於專案的」（`InProject`，專案的讀取用，讓沒有專案的列不必讀進來），依月份（`occurred_on` 所在月的第一天）、領域、專案、類別與它的父類別分組。領域的預算（`AreaSpendingOfMonth`）、專案的預算（`ProjectSpendingOf`）、一個月的類別（`TopLevelSpendingOfMonth`）與每月的總額（`TotalsByMonth`）都是 `domain/model` 的純函式，由這些列算出；repo 讀花了多少也只有這一個方法，use case 測試的假 repo 同樣只有這一個，回傳測試給的列、記下被問的條件（含 `InProject`），不篩、不排、不加總：什麼算花了、哪幾天、哪些列，是 PostgreSQL 整合測試的事（第 6 節）。
- **與領域的數字相等。** 一個月的 `Total` = 該月各領域的 `SpentWithProjects`（`GetAreaBudgets`）加上沒有領域的支出：沒有領域的支出不屬於任何領域，不在領域的任何數字裡，卻是這個月花的。兩邊現在由同一批列算出，所以這個相等在結構上就成立（domain 與 use case 的測試各有一個）；要證明的是那一批列是對的：adapter 的整合測試拿真的 SQL 回的列，對測試自己記下的沒有領域的支出（不拿 SQL 與自己比）與寫下來的數字（領域內、專案內、兩者皆無的支出，與手續費、分期利息、外幣、對帳，月份、類別各自相等），以及 `cmd/` 的端到端測試（真的 use case 與 PostgreSQL，確認、作廢與日期在未來的各一）。
- **「到哪一天」也只有一份。** `model.SpendingRange(第一個月, 最後一個月, 今天)`：從第一個月的第一天，到最後一個月的最後一天與今天中較早的一天，兩端都含；第一個月還沒開始就沒有範圍，不問 repo。`GetAreaBudgets`（行為不變）、`GetSpending`、`GetSpendingTrend` 都用它（use case 裡問 repo 的只有 `spentIn` 一處），今天來自注入的時鐘，與餘額同一個；專案不分月，所以 `GetProjectBudgets` 不用它，它問「到今天」，不設下限。月份的算術（`CountMonths`、`MonthsBetween`）與各種讀法（`TotalsByMonth`、`AreaSpendingOfMonth`、`ProjectSpendingOf`、`TopLevelSpendingOfMonth`）是 `domain/model` 的純函式。
- **參數。** `Month`、`From`、`To` 與 `GetAreaBudgets` 一樣只取它們所在的月（以自己的時區讀）。`GetSpendingTrend` 整個區間只問 repo 一次；`From` 或 `To` 沒給是 `ErrMonthRequired`（它的訊息不再提「預算」，因為這兩個讀取也用它），`To` 的月早於 `From` 的月是 `ErrDateRangeReversed`，超過 `usecase.MaxSpendingTrendMonths`（120）個月是 `ErrSpendingRangeTooWide`，都在讀 repo 之前就回。

**等著確認的草稿。** `GetDraftSummary{}` 回傳還沒確認的草稿：`Count`（每張草稿算一次）、`OwnTransfers`、`OwnTransferAmounts`、`Amounts` 與 `Months`（新的月份在前）；每個月有 `Count`、`OwnTransfers`、`OwnTransferAmounts`、`Amounts` 與 `Accounts`。讓人在確認一個匯入的月份之前先看到整體，不必把每張草稿都讀出來。草稿不改餘額、預算與報表（`CONTEXT.md`），所以這裡的金額是「如果每一張草稿今天都確認，各帳戶的餘額今天會怎麼變」。

- **什麼是一張草稿。** 狀態為草稿、而且不是分期計畫的期的交易。計畫（消費分期或帳單分期）算一張，由起點代表：確認、捨棄與作廢都以計畫為單位（給起點就帶著各期），所以不論有幾期都只算一張，算在起點那個月。各期在資料庫裡是各自的草稿，但不是各自要處理的東西，不算。已確認與作廢的不在其中，不論發生在哪一天；也不受「今天」限制，日期在今天之後的草稿一樣在等（算進張數，不算進金額，見下）。月份是 `occurred_on` 所在的月。
- **草稿屬於哪個帳戶，與 `ListTransactions` 的 `AccountID` 篩選是同一個定義：有分錄到那個帳戶，任何 book 都算。** 消費分期的起點只有 `instalment` 帳與 `expense` 帳的分錄，`instalment` 分錄到卡，所以是那張卡的草稿（但此刻卡一毛也沒扣：沒有 `account` 分錄，沒有金額）；帳單分期的起點有 `account` 分錄（把已出的帳單還清）與同一張卡的 `instalment` 分錄。一張草稿有幾個不同的帳戶，在每個帳戶底下各算一次，在月份與總數只算一次：轉帳在轉出與轉入的帳戶都有；手續費從轉出的帳戶付，那個帳戶仍只算一次，從第三個帳戶付，第三個帳戶也有這一張。沒有任何分錄到帳戶的草稿（別人替我付的支出），在該月「沒有帳戶」那一組（`AccountID` 為 nil，排在最後，沒有 `Amounts`），與 `WithoutAccount` 篩選是同一個定義。所以某月某帳戶的 `Count`，就是 `ListTransactions` 以該月（`From`、`To` 為第一天與最後一天）、該帳戶（或 `WithoutAccount`）、狀態草稿與 `ExcludePlanPeriods` 列出的張數，網頁上數字連到的清單（連結帶 `periods=none`）與數字一致（有測試：adapter 對十種種類各一張草稿與兩種計畫，`cmd/` 對真的 use case 與 PostgreSQL，含沒有帳戶那一組）。
- **金額：確認後今天會動多少。** 每個標的各一筆：`Outflow` 是為負的 `account` 分錄加總（以正數表示），`Inflow` 是為正的加總，`Net = Inflow − Outflow`，與餘額同號，負的是減少。只算 `occurred_on` 不晚於今天（時鐘）的交易，與 `GetBalances` 是同一條規則：到那天才進餘額。所以計畫的各期，扣款日到了的算，沒到的不算；到了的算在計畫的月份與被扣款的帳戶底下，不在各期自己的月份。標的依代碼排序（以位元組比較），帳戶依名稱再依 id 排序（名稱也以位元組比較），「沒有帳戶」最後。`cmd/` 的測試把它與真的餘額對起來：確認全部草稿之後，每個帳戶的餘額變動就是它在摘要裡的淨額。
- **自己帳戶之間的轉帳。** 轉帳把錢從一個自己的帳戶移到另一個。每一筆轉帳、每個標的，轉出與轉入（`account` 分錄）較小的那一邊，是在自己的帳戶之間移動的部分，不算進月份與全部的 `Outflow`、`Inflow`（本來就淨額為 0）；它在各個帳戶的金額裡，因為那兩個帳戶的餘額真的會動。手續費是剩下的部分，留在 `Outflow`（從轉出的、收款的或第三個帳戶付都一樣）。有這樣一個部分的草稿數是 `OwnTransfers`（月份與全部各一個），它們仍在 `Count`。只有轉帳算：換匯與買賣動兩種標的，每個標的都只有一邊；收入、往來、回饋與帳單分期的手續費若與進來的錢同一個標的，帳戶分錄也有進有出，但那不是錢在自己的帳戶之間移動。日期在今天之後的轉帳還沒動任何帳戶，沒有這個部分（在 `Count`，不在 `OwnTransfers`）。月份與全部的金額因此不是各帳戶金額的加總，而是由草稿算的。**這些錢是 `OwnTransferAmounts`**（月份與全部各一份，每個標的一筆，`Outflow` 與 `Inflow` 都是那個數、`Net` 為 0）：每個月、每個標的、`Outflow` 與 `Inflow` 各一欄，各帳戶的列加起來、減掉它，剛好是月份的 `Amounts`。網頁因此在帳戶的列與「這個月合計」之間放一列「自己帳戶之間的轉帳」，以負號寫這些錢，讓每一欄加得起來；頁面只寫 use case 給的數，不自己算。
- **純函式，與一條語句。** `TransactionRepo.DraftTransactions` 以一條語句（`ListDraftPostings`）讀出每一張狀態為草稿的交易（含計畫的各期，帶著 `PlanID` 與 `PlanSeq`）與它有帳戶的分錄（帳戶與標的的名稱與代碼、book、數量），沒有這樣的分錄的草稿就一列。一條語句讀的是同一份快照，所以不需要 `DB.WithinSnapshot`。哪些是草稿、算在哪個月與哪個帳戶、哪些分錄算進今天的金額、自己帳戶之間的部分與排序，都是純函式 `model.SummarizeDrafts(草稿, 今天)` 的事，由 `domain/model` 的單元測試逐條驗證，不需要資料庫；use case 只問時鐘、讀一次，並給每個金額 `Net`。沒有草稿是沒有月份、張數 0，不是錯誤；某一期的計畫起點不在草稿裡（資料不一致）回 `ErrMalformedPlan`。

**預覽餘額。** `GetPreviewBalances{}` 回傳每個帳戶、每個標的「如果每一張草稿今天都確認，今天會有多少」，`GetBalances` 的兩個數字各一份：`Held`（持有：`Quantity` 的預覽）與 `Unbilled`（未到期分期：`UnbilledInstalment` 的預覽）。每個數字有 `Total`（預覽）、`Confirmed`（已確認的部分）、`Drafts`（草稿加上的部分，可以是負的）與 `Basis`（`confirmed`：草稿沒動它；`both`：有已確認的部分、草稿動了它；`drafts`：沒有已確認的部分，整個是草稿的）。輸出另有 `DraftCount`（草稿的張數，與 `GetDraftSummary` 的 `Count` 同一個定義）與 `HasConfirmed`（任何一個數字有沒有已確認的部分）。帳本裡全是草稿時（例如匯入之後），`GetBalances` 什麼也沒有，總覽的餘額區因此是空的；草稿不改餘額（`CONTEXT.md`），所以這不是餘額，是確認之後會有的餘額，網頁把它標成預覽（`web-readonly.md` 第 4 節）。標題、說明與每個數字說的是同一件事（張數、有沒有已確認的、每個數字是什麼構成的），所以它們來自同一次讀取，頁面不自己判斷。

- **定義只有一個。** 每個數字各自 `Total = Confirmed + Drafts`。`Held.Confirmed` 是 `GetBalances` 今天的 `Quantity`，`Unbilled.Confirmed` 是它的 `UnbilledInstalment`（已確認、`occurred_on` 不晚於今天）。`Drafts` 是狀態為草稿（分期計畫各期在資料庫裡是各自的草稿，也算）、`occurred_on` 不晚於今天的交易的分錄，各在自己的帳戶：`account` 帳簿的加進 `Held`，`instalment` 帳簿的加進 `Unbilled`。`Held.Drafts` 與 `GetDraftSummary` 的金額同一條規則、各帳戶所有月份的 `Net` 加起來：轉帳在轉出與轉入的兩個帳戶都算；日期在今天之後的草稿與還沒到期的各期不算；作廢的不算；沒有帳戶的草稿不動任何帳戶。**確認全部草稿之後，每個帳戶每個標的的 `GetBalances`（`Quantity` 與 `UnbilledInstalment` 兩個）就是確認前的 `Total`。**
- **分期計畫的草稿，以及它與草稿區的關係。** 消費分期的起點只有 `instalment` 與 `expense` 帳的分錄：起點那天 `Unbilled.Drafts` 就是負的整筆本金（那張卡欠它），沒有 `account` 分錄，`Held` 不動；各期到期那天，那一期的本金從 `Unbilled` 移到 `Held`（`instalment` 帳為正的本金，`account` 帳為負的本金與利息）。帳單分期的起點有 `account` 分錄（把已出的帳單還清，`Held` 加本金）與同一張卡的 `instalment` 分錄（`Unbilled` 減本金）：兩邊的 `Drafts` 互相抵銷，各期到期時再把本金從 `Unbilled` 移到 `Held`。只算 `Held` 會讓卡欠的比確認之後少。`GetDraftSummary` 只算 `account` 帳簿，所以**草稿區的淨額 ＝ 所有帳戶 `Held.Drafts` 依標的的總和**（自己帳戶之間的轉帳淨額為 0），`Unbilled` 不在這個相等裡：分期計畫的本金不是帳戶的收支，網頁另外把它列在「未到期分期」那一行（`web-readonly.md` 第 4 節）。總覽的「未到期分期」表（`ListInstalmentPlans`）只含已確認的計畫；草稿的計畫確認之後才列在那裡，它們還沒到期的本金已經在帳戶的 `Unbilled`。
- **兩條語句，一份快照。** `TransactionRepo.PreviewBalances(day)` 回傳 `model.BalancePreview`（`Balances` 與草稿的張數 `Drafts`），在 `DB.WithinSnapshot`（唯讀、REPEATABLE READ）裡讀兩條語句：sqlc 的 `ListPreviewBalances`（`GetBalances` 的那條 SQL 加上草稿：`FILTER` 分出已確認與草稿、`account` 與 `instalment` 兩個帳簿共四個和，不必把每張草稿讀進來）與 `CountDrafts`（狀態為草稿、不是計畫各期的交易，與 `SummarizeDrafts` 的 `Count` 同一個定義，不限日期）。所以餘額與張數是同一個時刻：一張草稿在讀的當中被確認，只會在已確認或草稿其中一邊，不會兩邊都有或都沒有。**這有測試釘住**（與 `Statement` 的 `TestAStatementIsReadInOneSnapshot` 同一個做法）：一個 pgx tracer 在 `ListPreviewBalances` 讀完、`CountDrafts` 送出之前，讓另一條連線確認、記錄或捨棄一張草稿，讀到的餘額與張數仍是讀開始的那一刻，下一次讀才是新的；拿掉 `WithinSnapshot`，三個案例都失敗（改程式碼、跑測試、再還原，試過，不在程式碼裡），其餘預覽的測試仍會通過，所以它們釘不住這一點。有一列的是有已確認分錄（任一帳簿）或草稿的 `account` 或 `instalment` 分錄的帳戶與標的，分錄加總為 0 時仍列出。它與 `model.SummarizeDrafts` 是同一個規則的兩種寫法（一個只加總，一個要依月份與帳戶分組、處理自己帳戶之間的轉帳），所以測試把兩者對起來：repo 的整合測試在十二個日子上，把每個帳戶與標的的 `Held.Drafts` 對 `SummarizeDrafts` 各月份淨額的總和、兩個 `Confirmed` 對 `Balances`、所有帳戶的 `Held.Drafts` 對草稿的總淨額、`Drafts` 張數對 `Count`；逐日釘住草稿的消費分期與帳單分期的兩個數字；確認全部草稿之後各帳戶的 `Balances`（兩個數字）就是確認前的 `Total`；`cmd/` 的測試用真的 use case 做同一件事。SQL 的規則少了任何一條，這些測試都會失敗（日期的上界、草稿的 `instalment` 帳不算、張數把計畫各期也算：改 `.sql`、重新產生碼、跑測試、再還原，逐條試過，不在程式碼裡）。
- **use case。** 問時鐘的今天（與 `GetBalances` 同一個，時區也是），讀一次，不開資料庫交易；每個數字的 `Total` 與 `Basis` 是 `model.PreviewFigure` 算的（`Total()`、`Basis()`），`HasConfirmed` 是 `model.BalancePreview.HasConfirmed()`；沒有任何分錄是空的，不是錯誤。`Basis` 在 port 是它自己的型別 `usecase.PreviewBasis`（`PreviewBasisConfirmed`、`PreviewBasisBoth`、`PreviewBasisDrafts`），不是 `domain/model` 的：finance 以外的 context 只能 import finance 的 `usecase/port/in`（架構慣例第 4 節），拿來和 `Basis` 比較的字要在 port 裡；use case 把 model 的三個值各轉成同名的那一個，測試釘住。

**`ListTransactions` 的篩選。** 網頁的清單用到這幾個，都與其他篩選條件一起成立，分頁在篩選之後（都在 `listIDsSQL`，值一律是參數）：

- `Kind` 是十種種類之一（`expense`、`income`、`transfer`、`exchange`、`opening`、`reward`、`dealing`、`reconciliation`、`instalment`、`trade`）就只列該種類的交易，空字串列全部；其他值回 `ErrInvalidKind`，與 `Status` 一樣在問 repo 之前就檢查，訊息列出可用的值（多一個 `t.kind = $n`）。分期要注意：消費分期的起點種類是 `expense`，各期是 `instalment`；帳單分期的起點與各期都是 `instalment`。
- `AccountID` 列有分錄到該帳戶的交易，任何 book 都算（所以消費分期的起點列在它的卡底下）。`WithoutAccount` 列沒有任何分錄到帳戶的交易（`NOT EXISTS` 一個 `account_id` 不為 NULL 的分錄），例如別人替我付的支出。兩者是對方的另一邊，同時給是 `ErrConflictingFilters`，在問 repo 之前就回。
- `ExcludePlanPeriods` 不列分期計畫的各期：計畫只列它的起點，不在計畫裡的交易照列（use case 設 `TransactionFilter.WithoutPlanPeriods`，多一個 `(t.plan_seq IS NULL OR t.plan_seq = 0)`）。給了 `Status` 草稿又給了它，列出的草稿就是 `GetDraftSummary` 算的那些，計畫是它的起點。**狀態本身不隱藏各期**：各期是各自的交易，草稿計畫的各期也是草稿，所以只給 `Status` 草稿的清單照列各期，某個月出現「分期第 1 期、草稿」的列，用草稿篩選就找得到它。網頁的草稿頁連到清單時多帶 `periods=none`，清單上說明「只列分期計畫的起點」並附連各期一起列的連結。給了 `PlanID` 就列計畫的全部交易，起點與各期，不論狀態；與 `ExcludePlanPeriods` 一起給只剩起點。

## 6. 測試

- domain：分錄產生與不變條件（每個種類、外幣、多類別、不平衡時拒絕）。
- use case：以假的 port 測驗證規則與冪等。
- postgres adapter：整合測試，含 CHECK 限制、平衡檢查（不平衡的交易在提交時被拒絕）、餘額查詢只計已確認、草稿確認後餘額改變、作廢後餘額還原、捨棄草稿連分錄一起刪、全有或全無、併發重複冪等鍵。冪等與全有或全無的測試在 repo 層（`WithinTx` 加 `TransactionRepo`）：adapter 依 depguard 不能 import use case。
- `pkg/postgres`：`WithinTx` 的整合測試：提交、回滾（錯誤與 panic）、context 已帶著交易時回傳 `ErrNestedTx`（`fn` 不執行、開著的交易照常提交；`fn` 把錯誤回傳時整個回滾，連線乾淨地回到連線池）；`WithinSnapshot` 讀到的是開始時已提交的資料。
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
- M1c 改期：domain：新的各期照記錄時的做法（逐筆分錄、沿用起點的欄位、鍵與位置）、起點與被呼叫的計畫都不變、所有排程的拒絕（沒有期、日期、本金、利息與類別，總額不符且帶著兩個金額）、已作廢的計畫、各期與起點狀態不一致、標的不符、沒有 `instalment` 分錄的起點。過去的各期（草稿與已確認的計畫各一遍）：到期的各期原樣再送、剩下的放進一期（提前清償）不需要 `ChangePast`；少一期、在今天之前多一期、日期、本金、利息或利息的本位幣金額不同、有利息時換利息類別、沒有利息的期多出利息、同時改好幾期，都回 `*PastPeriodsChangedError` 並指出是哪幾期，帶 `ChangePast` 就成功；今天到期的期可以改，昨天到期的不行；沒有任何過去的期有利息時類別可以換。已封存的利息類別：計畫的各期現在用的放行，別的已封存的、系統類別仍被拒，沒有任何一期有利息的計畫沒有「現在用的」。use case：草稿與已確認的計畫（新各期的狀態與 `confirmed_at`）、舊各期已刪、起點不動、一個資料庫交易、鎖的順序、鎖住之後才發現作廢或被刪、各種拒絕都什麼也不寫、同一排程做兩次結果相同；過去的各期：到期的原樣再送不需要 `ChangePast`，各種改動沒有它就被拒且什麼也不寫（交易、刪除、寫入與狀態都不動），帶了就成功（草稿與已確認的各一遍），今天取自時鐘（到期那天可以改、隔天不行）；已封存的利息類別。postgres adapter：刪、寫、確認在一個交易裡取代排程，草稿與已確認的都行，餘額隨新的日子變，一期不平衡時整個取代還原、舊排程原樣。`cmd/` 端到端：12 期的計畫確認、三期過去後把剩下的 9,000 改成下個月一期（利息也改），到期的三期原樣再送，不需要 `ChangePast`：餘額、未到期分期、計畫列表與利息支出都對，總共欠的不變；同一個呼叫把過去一期的利息打錯就被拒（指出是第 1 期）、計畫與餘額照舊，帶 `ChangePast` 才改（那一期的利息支出從 30 變 33，卡多欠 3）；一期在今天前後來回移動（沒有 `ChangePast` 被拒，帶了才動）；作廢後拒絕；總額不符的錯誤帶著兩個金額；草稿的期移到對帳那天前後；利息類別在記錄後被封存，計畫仍可以用它改期，別的已封存的類別不行，計畫換了別的類別之後原來那個也不行。
- 計畫的併發（`cmd/finance_plan_concurrency_integration_test.go`，真的 PostgreSQL）：要看某一種交錯時，交錯是做出來的，不靠運氣：pgx tracer 把第一個呼叫停在它的第一個鎖之後，其他的在那時才啟動，每隔 5 毫秒問 PostgreSQL 它們是已經結束還是在等鎖（有 20 秒的期限），都有了答案才放第一個走。停住、輪詢與期限只寫了一份（`heldCalls`，在 `cmd/finance_concurrency_integration_test.go`），對帳與計畫的測試共用。兩個呼叫以相反的順序確認同樣兩個計畫：第一個全部確認，第二個等它、然後回 `ErrNotDraft`，沒有 `40P01`。改期握著起點的鎖時，確認或作廢要等它，等到時動的是新的各期（舊的各期已不在）；確認同時給了起點與它的各期（把整個計畫列出來再全部確認，會這樣送），等到時那些期已被換掉：回 `ErrNotFound` 並指名那幾期，不回一筆筆空的交易，計畫仍是草稿。要看的是「任何交錯都對」時，跑固定的回合數（20），呼叫同時啟動，沒有 sleep，`-race` 乾淨，每個呼叫有自己的期限（20 秒）：卡住的呼叫以 `context deadline exceeded` 結束，被當成沒有任何執行順序解釋得了的錯誤，測試失敗並指出是哪一個，不會掛到 `go test -timeout`：兩個計畫以相反順序確認、同時有人捨棄一個、作廢另一個；同一個計畫的兩個確認、一個作廢、一個捨棄；計畫確認與同一帳戶的對帳（對帳不是被草稿擋下，就是算到了那一期，不會拿過時的餘額）；隨機挑的多個計畫與一般草稿一起確認（含起點旁的期）；兩個改期與確認、作廢重疊。每回合檢查：沒有資料庫錯誤（尤其是死結 `40P01`）、一個計畫只有一種狀態、每一筆只被確認一次（tracer 數 `SetTransactionStatus` 到 `confirmed` 的次數）、各呼叫成功與否是某個執行順序留得下的。在副本上弄壞鎖時這些測試會失敗（不在程式碼裡）：起點與一般交易不依 id 排序，相反順序的測試以 `40P01` 失敗；拿掉 `FOR UPDATE`，或讓作廢先鎖一期再鎖起點，對同一個計畫的測試失敗；拿掉鎖住之後對「由起點涵蓋的期」的比對（與輸出迴圈的檢查），確認同時給起點與各期的測試失敗（呼叫成功、輸出是空的交易、計畫被確認）。
- 預算 domain：`NewAreaBudget`／`NewProjectBudget`（月取所在月的第一天、以月份自己的時區讀；沒有月；金額 0 可以、為負與位數太細不行；分配大於 0 且位數合法；加總剛好等於預算可以、多一分不行，訊息帶兩個金額；封存、收入方向、兩種系統類別、重複、與父類別同時有分配（不論誰先）各被拒，且指出是第幾筆；子類別單獨、手足、與別的父類別的分配並存都可以）、`MonthOf`／`LastDayOf`（含閏年二月）、`BudgetsInForce`（較早的列適用、晚於該月的列不適用、金額 0 結束且之後能再開始、各領域各自、輸入不照順序）、`NewBudgetStatus`（落入自己類別的分配、子類別落入父類別的分配、子類別自己有分配時只拿自己的、沒有分配的歸未分配、各分配加未分配等於 `Spent`、沒花的分配仍列出、剛好花完不算超過、金額 0 的預算花一分就超過、沒有預算時沒有超過且全部未分配；分配的 `Over`：剛好花完不算、多一分算、子類別的支出落入父類別的分配而讓它超過、子類別自己的分配只拿它自己的、沒有東西落入的不超過、與預算的 `Over` 互不相干）。花了多少的純函式（從 `SpendingRow` 讀出各種樣子）：`AreaSpendingOfMonth`（同一個領域與類別的幾列加總、屬於專案的另列且各專案併成一列、沒有領域的與別的月份的不算、月以自己的時區讀）、`ProjectSpendingOf`（不分月與領域加總、沒有專案的不算）、`TopLevelSpendingOfMonth`（子類別在父類別底下，不論父類別有沒有自己的支出：只花在父類別、只花在子類別、兩者都花；依 `Total` 再依 id、子類別依金額再依 id；一個類別的幾列（領域、專案各不同）加總；別的月份不算；沒有列時沒有類別、`Total` 為 0；月以自己的時區讀）、`TotalsByMonth`（沒有列的月份補 0、區間外的月份不算、反向的區間沒有月份、領域與專案各不同的列都算），以及三種讀法互相相等：各領域加沒有領域的 = 各頂層類別加起來 = 那個月的總額。
- 預算 use case：以假的 port（預算的存取與取代照真的語意，並拒絕資料庫交易以外的寫入；花了多少只有一個方法，回傳測試給的列、記下被問的條件，不篩、不排、不加總）與假的時鐘測：設定從所在月第一天起、取代同一個月且保留列的 id 與時間、不動其他月份與領域、一次資料庫交易、經 `VerifyReferences` 驗證領域或專案（找不到與已封存各一）、各種驗證的拒絕（兩種預算共用一張表）且什麼都不存；`RemoveProjectBudget`（拿掉後不再列出、再拿回 `ErrNotFound`、已封存的專案也能拿掉且不問 catalog）；`GetAreaBudgets` 的月份查找（較早的預算適用、金額 0 結束、晚於該月的預算不適用）、專案的支出不進領域的 `Spent` 與分配但進 `SpentWithProjects`、`Over` 只看 `Spent`、父類別的分配、分配的 `Over`（剛好花完不算、父類別的分配含子類別的支出、沒有東西落入的不超過）、未記錄類別與沒有分配的歸未分配、沒有預算只有支出與有預算沒有支出、依 catalog 順序（含已封存、不照 id）、沒有領域的列不產生領域、catalog 沒有的領域回 `ErrNotFound`、沒有東西可列時不問 catalog；被問的範圍（以假的 repo 記下的條件驗證）：月初、月末、下個月的第一天、時鐘時區（台北凌晨）、未來的月份不問 repo、範圍是「月初到月底與今天中較早的一天」，領域的讀取與一個月的支出問同樣的天；`GetProjectBudgets` 問「到今天」（時鐘時區）、不設下限、沒給專案時不限專案、金額 0 的預算、只有預算與只有支出、沒有專案的列不算、順序、單一專案（只經 `VerifyReferences`、不列 catalog、repo 只限那一個專案、找不到回 `ErrNotFound` 且不讀 repo、已封存的放行、沒有東西時是空的）。
- 花了多少的 use case（`GetSpending`、`GetSpendingTrend`）：假的 repo 給固定的列，驗證的是 use case 做的事，不是 repo 的篩選：沒給月份回 `ErrMonthRequired` 且不問 repo、月取所在月（以自己的時區讀）、被問的天（本月到今天、已過的月到月底、還沒開始的月份不問、時鐘時區）、頂層類別、`Own` 與 `Children`（子類別在父類別底下，不論父類別有沒有自己的支出；依 `Total`、同額依 id）、`Total` 是各頂層類別加起來、沒花錢的月份是空的而不是錯誤、repo 的錯誤原樣回（領域與專案的讀取也是）、領域加沒有領域的 = 月總額；趨勢：每個月一列、最舊的在前、沒有東西的月份是 0、只問 repo 一次、各種拒絕（缺月份、反向、超過 120 個月）都在問 repo 之前、與各月的 `GetSpending` 一致。
- 預算 use case，已封存的領域：預算在領域封存後仍在效力中、照列（十二月與很久以後的月份都列）；設金額大於 0、為負、或金額 0 但有分配都回 `*ReferenceError`（已封存）且什麼都不存；金額 0 且沒有分配的結束成功，仍問 catalog 一次領域在不在，回的預算從所在月第一天起、金額 0、沒有分配；結束後十二月沒有預算但仍列出它花的，十月與十一月還有原來的預算，很久以後沒有東西可列；結束後再設大於 0 仍被拒；catalog 沒有的領域對結束一樣被拒（`*ReferenceError`，不是已封存），出錯的 catalog 的錯誤原樣回，不當成已封存。
- 預算 postgres adapter（真的 PostgreSQL）：`fin_budget` 的各個 CHECK（`scope` 與欄位的對應、月的第一天、金額不為負）與兩個 `UNIQUE`、`fin_budget_allocation` 的 CHECK、主鍵、兩個外鍵與連帶刪除；儲存（取代金額與分配、保留列的 id 與時間、不動其他月份、半途失敗時整個還原、依類別名稱再依 id 的順序）與同時存同一個月的併發測試；`DeleteProjectBudget`（連分配一起刪、沒有就是 `ErrNotFound`、領域的 id 刪不到）；`AreaBudgets` 與 `ProjectBudgets`（依領域再依月份、有與沒有分配、專案的預算不在領域的裡面、依專案篩）；花了多少（`Spending` 那一條語句，直接看它回的列）：每個月、領域、專案、類別各一列（同一組的兩筆加總，沒有領域與沒有專案的列在，那一欄是空的）、月界（第一天與最後一天含、前後一天不含、單日範圍；沒有下限時是到上限為止的每一天）、日期在未來的到那天才算、只算已確認（草稿與作廢都不算、確認後算、作廢後不算，只有草稿的月份沒有列）、可只問一個專案（只有它的列、與日期一起成立、沒有支出的專案沒有列）、月是該月第一天的 UTC 午夜日期而年底與年初分開、子類別的列帶著父類別而頂層的沒有、一筆支出的多個類別各一列、已封存類別的支出仍在、手續費與分期利息照算（利息在各期的扣款日，之前不算；帳單分期的各期利息也是）、帳單分期的起點不算、對帳的「未記錄」照算且沒有父類別、分攤只算自己的份、別人替我付的算我的、外幣用本位幣金額、折扣不減、回饋與收入不減、沒有支出時沒有列；真的資料庫回來的列交給 domain 的純函式，算出的領域與專案讀取與原來各自一條語句時的結果一樣（領域依領域與類別加總並分開專案的、專案不分月且可只問一個），子類別的支出落入父類別的分配（含它超過時的 `Over`）；一個月的總額 = 各領域的 `SpentWithProjects` 加沒有領域的支出（測試自己記下沒有領域的，不拿 SQL 與自己比）。
- migration：`0007` 往下再往上，與只套用到 `0006` 的資料庫比對 catalog；有預算時 Down 中止且不動資料（領域單獨、專案單獨、兩者並存且帶分配各一個案例：只看其中一種 scope 的守衛會漏掉另一種，把守衛縮成只看專案時，領域單獨的案例會失敗，這是在副本上試過的，不在程式碼裡）；帳本裡的交易不擋 Down。
- `cmd/` 的端到端測試（預算，`cmd/budget_integration_test.go`）：真的模組、真的 PostgreSQL 與可控的時鐘。領域：先設各月的預算與分配，記下並確認支出（午餐是食物的子類別、轉帳的手續費、對帳少的 120、12 期的消費分期與它的利息、日期在未來的房租、專案的支出、草稿、記了又作廢的、10 月最後一天與 12 月第一天），讀十一月的狀態（分配、未分配、`Spent` 與 `SpentWithProjects`），十月、九月與十二月，時鐘走到 20 日（未來的房租算進來）、花超過（`Over`，沒有任何東西被擋）、12 月 4 日與 5 日（利息只在到期那天算進十二月、十一月不變）、金額 0 的預算結束預算、作廢分期後十一月與十二月都少掉它。專案：專案的支出（含沒給領域而帶入預設領域的）不計入領域的預算、整個專案的預算不分月、超過、日期在未來的到那天才算、拿掉預算後仍列出、找不到的專案、已封存的專案仍列出且不能再設預算。規則：各種被拒的預算（領域與專案各一遍）都不留下東西，之後能設，再設同一個月會取代。
- `cmd/` 的端到端測試（已封存領域的預算，同一個檔案）：設了預算、有十一月與日期在十二月的支出後封存領域，預算在十一月、十二月與很久以後的月份（2030 年 6 月）仍照列；大於 0 與金額 0 帶分配被拒、預算不變；從十二月起金額 0 的結束成功，十二月沒有預算但仍列出它花的，十一月仍有原來的預算，2030 年 6 月沒有東西可列；結束後再設大於 0 仍被拒。
- `cmd/` 的端到端測試（花了多少與分配的 `Over`，`cmd/spending_integration_test.go`）：真的模組、真的 PostgreSQL 與可控的時鐘。一個月的總額 = 各領域的 `SpentWithProjects` 加沒有領域的支出（領域內、專案內、兩者皆無的支出，與手續費、外幣、12 期的消費分期與它的利息、日期在未來的房租、草稿、記了又作廢的；時鐘走到 20 日、12 月 4 日與 5 日；作廢分期後兩個月都少掉它），各月的趨勢與該月的 `GetSpending` 一致，每個頂層類別的 `Total` 是它自己加底下的子類別、各頂層類別加起來是 `Total`；另一個月記了只花在父類別、只花在子類別、兩者都花、花了又封存的類別，驗證 `TopLevel` 的 `Total`、`Own`、`Children` 與順序，草稿與作廢的不在其中；分配的 `Over`：剛好花完不算、多一分算、子類別的支出讓父類別的分配超過、預算本身沒超過（領域與專案各一）。
- 每條規則的測試在沒有那條規則時都會失敗：月界（use case 被問的範圍與 SQL 的兩端）、專案的支出不進領域的 `Spent`（domain 與 use case 的 `InProject`）、父類別的分配（domain 的歸入、SQL 回傳父類別）、分配的 `Over`（剛好花完不算）、子類別在父類別底下與頂層類別的順序（domain）、日期在未來的到那天才算（use case 問的「到今天」與 SQL 的上界，領域與專案各一）、專案的讀取不設下限且把專案傳給 repo（use case）、月份的查找（晚於該月的不適用、金額 0 結束、取最大的一列）、各種驗證、草稿與作廢、手續費與利息、外幣、回饋與收入，以及已封存領域可以結束預算的例外（拿掉例外、結束時不問 catalog、任何金額都算結束、有分配也算結束、catalog 沒有的領域或任何錯誤也放行、對任何金額都放行已封存的領域；use case 的測試對每一種都失敗，`cmd/` 的測試對其中的拿掉例外、任何金額、有分配、對任何金額放行已封存的領域會失敗）。這是在一份拷貝裡逐條拿掉規則（改程式碼或 SQL）、跑測試試出來的，不在程式碼裡：每一條都有至少一個測試失敗；月界在 `cmd/` 的端到端測試裡也會失敗，所以那個測試放了 10 月最後一天與 12 月第一天的支出。「花了多少」的那一條 SQL 也這樣試過：草稿算、作廢算、上界或下界差一天、沒有專案篩選、沒有開放的下限、用數量不用本位幣金額、不切月、收入也算、領域或專案或父類別那一欄固定為空、不算已封存的類別，每一個都有整合測試失敗；domain 與 use case 也試過：分配的 `Over` 剛好花完也算、子類別不放在父類別底下、父類別的 `Own` 不算、領域的讀取算進沒有領域的列或不分月、專案的讀取算進沒有專案的列、頂層類別依 `Own` 排、子類別由小到大、專案的讀取給了下限或沒把專案傳給 repo、`Over` 沒帶到 port，每一個都有測試失敗。回饋分錄沒有類別，與類別的 JOIN 本來就排除它，所以把 `book` 條件放寬到回饋不會有測試失敗，那不是這條 SQL 的規則。
- M1d domain：結帳日期的算術（`DateInMonth`：該日或較短月份的最後一天，含閏年與 2100 年；`StatementContaining`：結帳日當天在它結束的那張帳單、前一天與隔天、第一天、月底、閏年的二月、跨年；以 1 到 31 的每個結帳日、2026 到 2029 年的每一天逐日驗證「每一天剛好在一張帳單上、第一天與結帳日期各在同一張、下一張從隔天開始、結帳日期是結帳日或月底」；`StatementsClosingBetween`：兩端都含、差一天就不含；`DueDate`：嚴格晚於結帳日期的第一個繳款日、繳款日大於、等於與小於結帳日、短月取月底、跨年；`Closed`：結帳日期早於今天；`NewStatementFigures`：`Closing = Previous − Charges + Credits`）；帳戶的日子（只有卡、有繳款日要有結帳日、1 到 31 的兩端與兩端外、兩個都沒有也可以、`WithStatementDays` 兩個一起取代且不改原帳戶、清掉不能有日子的帳戶不算錯、不共用呼叫端的指標）；入帳日（草稿與已確認都能設定、更改、清除；取它自己的時區讀到的日，台北 20 日零點半是 20 日，存成 UTC 零點的日期；同一天可以、早一天不行；沒有卡的帳戶分錄、作廢、分期計畫的起點與各期被拒，設定與清除各一遍，原交易不變；不改任何分錄、金額與狀態，餘額、預算與往來照舊；確認與作廢保留它；`Validate` 擋下早於交易日期與計畫的交易；`NewExpense` 帶入帳日）。
- M1d use case：以假的 port 與假的時鐘測。`SetPostingDate`：草稿與已確認的設定、更改、清除；給它已有的值成功且什麼都不變；只動入帳日；被拒的情形（沒有卡的分錄、作廢、計畫的起點與各期、早於交易日期、找不到）都不寫；計畫的交易被拒且**不上鎖**；鎖住之後才看狀態（鎖回來的交易與先前不鎖地讀到的不同時，以鎖回來的為準）；以日期自己的時區取日。`RecordExpense` 帶入帳日與被拒的情形（現金支出、早於交易日期、分期）不存任何東西。`GetCardStatement`：預設是今天開著的那張、給一天就是涵蓋它的那張（已結帳的與未來的）、結帳日當天未關而隔天關、繳款日、沒有繳款日、各標的的 `Previous`、`Charges`、`Credits`、`Closing`、交易的順序與被移到下一張的入帳日、日期不晚於今天（今天以時鐘的時區讀：台北凌晨還是當天，不是 UTC 的前一天）、草稿張數與日期在未來的草稿、repo 失敗時帶著它在讀什麼的錯誤；不是卡、沒有結帳日與不存在的帳戶在讀 repo 之前就被拒。`ListCardStatements`：新的在前、兩端都含、範圍內沒有結帳日期是空清單、一次讀全部的數字、一張的 `Closing` 是下一張的 `Previous`、兩端以各自的時區取日、各種拒絕（沒給、反向、超過十年、剛好十年可以）。帳戶：`CreateAccount` 帶兩個日子、各種不能有的日子不寫、`SetAccountStatementDays` 設定、更改、清除、已封存的卡、被拒的不動帳戶、repo 失敗。
- M1d postgres adapter（真的 PostgreSQL）：`fin_account` 的四個 CHECK（逐一，含兩端與兩端外），建立與 `SetStatementDays` 各被表擋一次，且擋下時帳戶不變；`SetStatementDays` 取代兩個日子、清掉的是 NULL、不動封存與名稱；`fin_transaction_posted_on_check`（`Insert` 與 `SetPostedOn` 各一，同一天可以）與 `fin_transaction_posted_on_plan_check`（以 SQL 直接更新與以 `Insert` 寫入，計畫的起點與各期都被擋下，更新後仍沒有入帳日）；`posted_on` 經 `Insert`、`Get`、`List` 來回。帳單的查詢：兩端（第一天與結帳日期在，前一天與隔天不在）、`posted_on` 把交易移到下一張並移出上一張，而 `Balances` 在結帳日仍算它；入帳日在下一張帳單的第二天時，交易在入帳日那一張、不在交易日期那一張；草稿與作廢不算數字，草稿算在 `Drafts`（一張草稿只算一次，日期在今天之後的也算）；日期在未來的到那天才算（交易日期與入帳日各一）；分期的期在扣款日、起點與未到期分期不在、帳單分期的起點是貸方；繳款、退款與回饋是貸方，轉出與手續費是借方；兩個標的各有一組；沒有活動的標的不列；`Previous` 加 `Credits` 減 `Charges` 等於 `Closing`，且一張的 `Closing` 等於下一張的 `Previous`（以另一種加法從存下來的交易算出來，驗證每一張）；其他帳戶不算；交易的順序（帳單日期、交易日期、由舊到新，同一天先記的在前）；以及一份快照（pgx tracer 在讀完數字的語句之後，讓另一條連線確認一筆草稿並刪掉另一筆：這一次的回答裡，交易與草稿張數仍是讀數字時的那一份，與數字加得起來；再讀一次才是現在的樣子）。
- migration：`0008` 往下再往上，與只套用到 `0007` 的資料庫比對 catalog；Down 守衛的案例（結帳日、結帳日加繳款日、拿掉 CHECK 才放得進去的只有繳款日、入帳日）各自中止且不動資料；沒有這些值的卡與帳本不擋 Down。`latestVersion` 改成 8，`0005`–`0007` 的測試不必改（`atVersion` 從最新的版本往下退）。
- `cmd/` 的端到端測試（M1d，`cmd/card_statement_integration_test.go`）：真的模組、真的 PostgreSQL 與可控的時鐘。結帳日 5 號、繳款日 20 號的卡：記支出時就帶入帳日、確認後才用 `SetPostingDate` 移到下一張（結帳日當天的消費）、更改與清除；讀兩張帳單與清單（數字、交易的順序、`Closed`、繳款日、一張的 `Closing` 是下一張的 `Previous`）；有入帳日與沒有時，結帳日的餘額與各月的預算一樣；草稿張數；時鐘走到結帳日當天與隔天，帳單從未關到已關。被拒的入帳日（現金的支出、早於交易日期、分期計畫的起點與期、作廢）什麼都沒留下，每一個回的都是各自該回的錯（`errors.Is`）。第 5 節的兩個後果：結帳日改成 10 號後過去的帳單重切；對帳仍以交易日期比對，結帳日當天的消費被移走後，對帳那天的記錄餘額仍含它，與銀行的帳單差那一筆。
- `cmd/` 的併發測試（M1d，`cmd/finance_statement_concurrency_integration_test.go`，真的 PostgreSQL，與計畫的併發測試同一套做法：交錯是做出來的，不靠 sleep）：日期與作廢在同一筆交易上，各以一邊握著鎖（作廢先，日期等它、回 `ErrTransactionVoid`；日期先，作廢等它、作廢的交易帶著日期）；與確認各以一邊先後（都成功、已確認且有日期）；與捨棄（日期等到時交易已不在，`ErrNotFound`）；一筆草稿上同時進來的確認、兩個不同的日期、作廢與捨棄，`stressRounds`（20）回合，每個呼叫有自己的期限：沒有資料庫錯誤（尤其是 `40P01` 死結），每個呼叫只得到成功或它看到的狀態的拒絕，而且**存在某一個先後順序**（120 種全試），照它一個一個做，每個呼叫的結果與最後交易的狀態都與實際相同；分期計畫的各期與起點上的日期，在計畫被確認與改期的同時一律被拒（`ErrPartOfPlan`，或被改期換掉的期 `ErrNotFound`）、不死結、沒有任何一筆計畫的交易有日期；另一條連線鎖住整個計畫時，計畫的期的日期仍立刻被拒，不等；另一條連線鎖住一般交易時日期等它，放開之後才設。
- M1d：每條規則的測試在沒有那條規則時都會失敗。這是在一份拷貝裡逐條拿掉規則（改程式碼或 SQL）、跑測試試出來的，不在程式碼裡；每一條都有至少一個測試失敗：結帳日期算成下一張帳單的第一天；沒有月底（31 號溢到下個月）；帳單的第一天差一天（兩種方向）；繳款日取結帳日當天；`Closed` 在結帳日當天就算；`Closing` 的算術；SQL 忽略入帳日（三條查詢一起，以及各自單獨：數字、交易清單、草稿張數）；數字沒有「不晚於今天」的上界、交易清單沒有（Go 的 `countedTo`）；結帳日期被排除；第一天被算進上一張的 `Previous`；草稿或作廢的交易也算進數字；作廢的也算草稿張數；所有 book 的分錄都算進帳單（分期與支出的分錄）；`Statement` 不用快照；帳單的交易倒過來排（帳單日期、交易日期、id 三個鍵各自改成新的在前：adapter 的順序測試每一種都失敗，前兩種在 `cmd/` 的端到端測試裡也失敗）；`SetPostingDate` 不重讀鎖回來的交易、先上鎖才分類（計畫的期被鎖）；`CheckPostedOnAllowed` 不再拒絕計畫的交易或作廢的（以及兩處都拿掉對計畫的拒絕）；`WithPostedOn` 不問卡；入帳日與交易同一天被拒、早於交易日期被放行；`nil` 不再清除入帳日；`StatementDate` 不看入帳日；`RecordExpense` 丟掉入帳日；`GetCardStatement` 以所問的那一天而不是今天為準；`ListCardStatements` 由舊到新、不限範圍；migration 的 Down 守衛各漏一個條件（繳款日、入帳日、結帳日與繳款日）；CHECK 各被放寬（`posted_on` 不能與交易同一天、任何帳戶都能有日子、繳款日不需要結帳日、結帳日只到 30，以及計畫的交易可以有入帳日：拿掉 `fin_transaction_posted_on_plan_check`，或只放過起點，或只放過各期）。入帳日移動帳單、兩端（第一天與結帳日期）與「不晚於今天」這幾條最容易寫錯的規則，在 `cmd/` 的端到端測試裡也會失敗；計畫的交易被拒，在 domain、use case 與 `cmd/` 都有測試失敗；`SetPostingDate` 的鎖，use case 的測試與 `cmd/` 的併發測試各自會失敗。
- 等著確認的草稿與清單的篩選（網頁用的讀取）：domain 是純函式 `model.SummarizeDrafts`，不需要資料庫。計畫不論 1、3 或 12 期都是一張，算在起點的月份，各期不自成月份；到期的期算在計畫的月份與被扣款的帳戶，扣款日當天算、前一天不算；日期在今天之後的草稿在張數、不在金額（當天算、前一天不算）；轉帳在各帳戶的金額、不在月份與全部的金額，手續費（從轉出、收款、第三個帳戶付，或是另一個標的）留在 `Outflow`，每一筆轉帳各算各的，只因轉帳而動的標的不在月份的金額；換匯、買賣，以及手續費與進來的錢同一個標的的收入、往來與帳單分期，都不是自己帳戶之間的轉帳；日期在今天之後的轉帳不算在 `OwnTransfers`；自己帳戶之間的錢是 `OwnTransferAmounts`（每一筆轉帳每個標的較小的那一邊，`Outflow` 與 `Inflow` 各是那個數，手續費、還沒到期的轉帳與別種種類都不在其中），每個月、每個標的、`Outflow` 與 `Inflow` 各一欄：各帳戶的列加起來減掉它，剛好是月份的金額，全部的是各月加起來（`model` 的測試逐欄驗證，網頁的測試對頁面上的表驗證同一件事）；草稿屬於它有分錄的每個帳戶（含 `instalment` 帳）且每個帳戶只算一次，沒有帳戶的排在最後；月份、帳戶名稱（同名依 id）、標的代碼（位元組）的順序，與草稿的給法無關；沒有起點的期是 `ErrMalformedPlan`；審查者的五筆紀錄（原本顯示 8 張、流出 9,750、淨額 −6,750）是 5 張、流出 750、淨額 −750。use case：以假的 repo 給定的列與假的時鐘（含時區的清晨）：今天取自時鐘、每個金額有 `Net`、`OwnTransfers` 與 `OwnTransferAmounts` 傳出去、只讀一次、不開資料庫交易、錯誤包著說明往外傳；篩選：`ErrConflictingFilters` 在問 repo 之前回、`WithoutAccount` 原樣傳下去、只有給了 `ExcludePlanPeriods` 才要 repo 不列各期，只給狀態為草稿的清單不要，給了 `PlanID` 也照它的意思。postgres adapter（真的 PostgreSQL）：`DraftTransactions` 只有草稿（已確認、作廢、確認或捨棄之後都不在，不論日期）、有帳戶的分錄與它們的帳戶名稱和標的代碼（含 `instalment` 帳，不含別的 book）、沒有帳戶的草稿一列、計畫的起點與各期帶著 `PlanID` 與 `PlanSeq`；`WithoutAccount`、`WithoutPlanPeriods`、`AccountID` 比對任何 book 的分錄；十種種類各一張草稿加兩種計畫：摘要的每個張數都是 `ExcludePlanPeriods` 的清單找到的張數。`cmd/` 的端到端測試（`cmd/drafts_integration_test.go`）：審查者的五筆紀錄，在 11 月 14 日與計畫第一期到期的 12 月 5 日，確認全部草稿之後每個帳戶的餘額變動就是摘要裡它的淨額；各種形狀的草稿，每個月份與帳戶（含沒有帳戶）的張數都是清單找到的，確認起點之後整個計畫離開摘要與清單；給了 `ExcludePlanPeriods` 的草稿清單只有起點，只給狀態為草稿的清單與計畫的清單有各期（12 月的草稿清單有計畫的第 1 期，給了 `ExcludePlanPeriods` 就沒有）；帳戶與沒有帳戶同時給是 `ErrConflictingFilters`。每條規則的測試在沒有那條規則時都會失敗，這是在一份拷貝裡逐條拿掉規則（不在程式碼裡）試出來的：計畫的各期算成草稿；各期算在自己的月份；沒有「不晚於今天」；沒有自己帳戶之間的部分，或者每一種種類都有；`OwnTransferAmounts` 少了一種轉帳或把手續費算進去（各欄加不起來）；只看 `account` 帳的分錄決定帳戶；一筆草稿在同一帳戶下每個分錄各算一次；少了沒有帳戶的那一組；沒有略去只因轉帳而動的標的；各種排序；沒有起點的期被悄悄略過；今天取自 UTC 的日子；`Net` 不對、`OwnTransfers` 沒傳出去、模型的錯誤被吞掉；SQL 沒有狀態條件、只讀 `account` 帳的分錄、不留沒有帳戶的草稿、不讀各期、同一筆交易的列沒有併在一起、帳戶篩選只看 `account` 帳、拿掉 `WithoutAccount` 或 `WithoutPlanPeriods`、狀態為草稿就隱含 `WithoutPlanPeriods`（12 月的草稿清單找不到第 1 期）。
- 預覽餘額（`GetPreviewBalances`）：use case 以假的 repo 給固定的列：每個數字的 `Total` 是 `Confirmed` 加 `Drafts`、`Basis` 依構成（草稿沒動、有已確認的而被動、整個是草稿的）並說成 port 自己的 `usecase.PreviewBasis`、`DraftCount` 與 `HasConfirmed` 照 repo 給的與列算出（含已確認的部分被草稿抵銷的仍算已確認）、順序照 repo 給的，只問 repo 一次、不開資料庫交易，問的是時鐘在自己時區的今天（台北凌晨與 UTC 的日期不同時），沒有東西是空的而不是錯誤，repo 的錯誤帶著做了什麼原樣回；`domain/model` 的純函式（`Total`、`Basis`、`HasConfirmed`）各有單元測試。postgres adapter（真的 PostgreSQL）：已確認與草稿各自的和（作廢的不在、日期在今天之後的不在、當天在）、張數（含日期在今天之後與沒有帳戶的）、確認一張草稿是把它從一個和移到另一個、轉帳在兩個帳戶（手續費從轉出的帳戶或第三個帳戶付）且全部加起來只剩手續費、草稿的消費分期起點那天就欠整筆本金（`Unbilled`），各期到期那天才動那張卡的 `Held`、帳單分期的起點在兩個數字都算且兩邊的草稿部分加起來為零、作廢的計畫兩邊都不在；兩條語句在一份快照裡（tracer 在餘額讀完、張數送出之前，讓另一條連線確認、記錄或捨棄一張草稿，讀到的餘額與張數仍是讀開始的那一刻，下一次讀才是新的；拿掉 `WithinSnapshot`，三個案例都失敗）；逐帳戶逐標的對 `SummarizeDrafts` 與 `Balances` 比對（每一種草稿各一張，加上已確認、作廢、日期在今天之後與沒有帳戶的）；確認全部草稿之後 `Balances` 的 `Quantity` 與 `UnbilledInstalment` 就是確認前的預覽。
