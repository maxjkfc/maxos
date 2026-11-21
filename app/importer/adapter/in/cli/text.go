// Package cli writes what the import commands print: the plan report, and what apply,
// confirm, verify and status did, in Traditional Chinese with the terms of the glossary
// (CONTEXT.md). It is the inbound adapter of the importer context for the command
// line; the commands themselves, which are Cobra's, are in cmd/.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// kindText names what a unit is recorded as.
var kindText = map[model.UnitKind]string{
	model.KindExpense:        "支出",
	model.KindExpenseReward:  "支出＋回饋",
	model.KindIncome:         "收入",
	model.KindReward:         "回饋",
	model.KindDealing:        "往來",
	model.KindTransfer:       "轉帳",
	model.KindExchange:       "換匯",
	model.KindTrade:          "買賣",
	model.KindOpening:        "期初餘額",
	model.KindBillInstalment: "帳單分期",
}

// kindOrder is the order kinds are listed in.
var kindOrder = []model.UnitKind{
	model.KindExpense, model.KindExpenseReward, model.KindIncome, model.KindReward, model.KindDealing,
	model.KindTransfer, model.KindExchange, model.KindTrade, model.KindOpening, model.KindBillInstalment,
}

// reasonText says why rows are not recorded and what to do about them.
var reasonText = map[model.Reason]struct{ Why, Proposal string }{
	model.ReasonUnknownType: {
		"這種記錄類型匯入程式不認得",
		"先確認它是什麼；在那之前不匯入，餘額會與 CSV 差這幾列的影響",
	},
	model.ReasonBadSign: {
		"金額的正負號與記錄類型不符",
		"確認原始資料；目前不匯入。若確定要記，在 mapping 的 rows 以 treatment 指定",
	},
	model.ReasonZeroAmount: {
		"金額為 0 或折算後沒有金額，帳本不收",
		"不需處理；沒有金額就沒有餘額影響（手續費若有，列在影響欄）",
	},
	model.ReasonTooManyDecimals: {
		"小數位數超過標的的位數",
		"調高 mapping 中該標的的 decimals 後再 plan",
	},
	model.ReasonZeroQuantity: {
		"買賣的非幣別標的，匯出的數量是 0，沒有數量就不能記成買賣",
		"預設不匯入：付款帳戶的餘額會比 CSV 多出付出的金額。查到數量後，在 mapping 的 rows 填 quantity，就會記成買入／賣出",
	},
	model.ReasonNotCurrencies: {
		"兩邊都不是幣別，帳本沒有這種交易",
		"預設不匯入；要記就分開記成買賣",
	},
	model.ReasonUnpaired: {
		"找不到另一半的轉出／轉入",
		"預設不匯入；到 MOZE 補上另一半後重新匯出，或在帳本手動記",
	},
	model.ReasonAmbiguousPair: {
		"同一分鐘的轉出／轉入有不只一種配法，不猜",
		"預設不匯入；在帳本手動記，或把其中一筆在 MOZE 改一分鐘",
	},
	model.ReasonAmountMismatch: {
		"同一個標的，轉出與轉入的金額不同",
		"預設不匯入；差額可能是手續費，請到 MOZE 把手續費記在 手續費 欄",
	},
	model.ReasonSameAccount: {
		"轉出與轉入對到同一個帳戶（同標的）",
		"預設不匯入；若兩個 MOZE 帳戶不該合併，改 mapping 的帳戶名稱",
	},
	model.ReasonInstalmentUnmatched: {
		"帳單分期／分期還款的列湊不成一個分期計畫",
		"預設不匯入；利息列已另記成支出。可在帳本手動記分期",
	},
	model.ReasonInstalmentAccounts: {
		"分期的列湊得起來，但在 mapping 中是不同的帳戶，分期計畫只能在一個帳戶",
		"把這些 MOZE 帳戶在 mapping 的 accounts 指到同一個帳戶名稱後再 plan，就會記成分期計畫",
	},
	model.ReasonSkipped: {
		"mapping 的 rows 指定略過",
		"照你的指定",
	},
	model.ReasonNoDate: {
		"這列需要日期，檔案裡沒有",
		"預設不匯入",
	},
	model.ReasonNoParty: {
		"往來沒有對象",
		"在 mapping 補上對象",
	},
	model.ReasonNoCategory: {
		"沒有類別",
		"在 mapping 補上類別",
	},
}

// pairingReasons are the reasons that are problems of pairing 轉出 and 轉入.
var pairingReasons = map[model.Reason]bool{
	model.ReasonUnpaired:       true,
	model.ReasonAmbiguousPair:  true,
	model.ReasonAmountMismatch: true,
	model.ReasonSameAccount:    true,
	model.ReasonNotCurrencies:  true,
}

// caveatText says what to know of rows that are recorded.
var caveatText = map[model.CaveatKind]struct{ Title, Note string }{
	model.CaveatInvestment: {
		"投資的列",
		"匯出沒有數量，不能記成買賣；照原本的類型記成支出／收入。帳戶餘額與 CSV 一致，但持有的標的不會出現在淨資產",
	},
	model.CaveatBrokerage: {
		"證券戶的列",
		"帳戶以台幣市值記錄、沒有數量，不是買賣；帳戶餘額與 CSV 一致，之後可在有數量時改記成買賣",
	},
	model.CaveatDiscountOverPrice: {
		"折扣大於金額的支出",
		"記成原價的支出，加一筆折扣金額的回饋，帳戶最後的餘額與 CSV 一致",
	},
	model.CaveatSameInstrumentExchange: {
		"同標的的「兌換」",
		"換匯需要兩種幣別，所以記成轉帳",
	},
	model.CaveatPendingIncome: {
		"待確認的收款",
		"預設記成類別「收款（待確認）」的收入、貼標籤「待確認」；要改，在 mapping 的 rows 用列的 key 指定（income、dealing、reward 或 skip）",
	},
	model.CaveatRefund: {
		"退款",
		"帳本沒有退款這個種類；預設記成類別「退款」的收入，原本的類別留在備註",
	},
	model.CaveatOpeningDate: {
		"期初餘額的日期不是由該帳戶自己的列決定",
		"帳戶沒有任何有日期的列，日期取同一個帳戶（或整個檔案）最早的列的前一天",
	},
}

// money writes an amount with the decimals of its instrument.
func money(q decimal.Decimal, decimals int) string {
	return q.StringFixed(int32(decimals))
}

// signed is money with a sign for what is not negative.
func signed(q decimal.Decimal, decimals int) string {
	s := money(q, decimals)
	if q.IsPositive() {
		return "+" + s
	}
	return s
}

// table writes a Markdown table.
func table(w *strings.Builder, headers []string, rows [][]string) {
	fmt.Fprintln(w, "| "+strings.Join(headers, " | ")+" |")
	fmt.Fprintln(w, "|"+strings.Repeat("---|", len(headers)))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = strings.NewReplacer("|", "\\|", "\n", " ").Replace(c)
		}
		fmt.Fprintln(w, "| "+strings.Join(cells, " | ")+" |")
	}
	fmt.Fprintln(w)
}

// clip shortens text to n runes for a table.
func clip(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// flush puts what was written on the writer.
func flush(w io.Writer, b *strings.Builder) error {
	_, err := io.WriteString(w, b.String())
	return err
}
