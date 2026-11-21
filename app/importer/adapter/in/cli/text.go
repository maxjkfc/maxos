// Package cli writes what the import commands print: the plan report, and what apply,
// confirm, verify and status did, in Traditional Chinese with the terms of the glossary
// (CONTEXT.md). It is the inbound adapter of the importer context for the command
// line; the commands themselves, which are Cobra's, are in cmd/.
//
// The plan and the use cases say what is the matter with codes and the values the text
// fills in (model.ProblemCode, model.Reason, …); the words are here, so that everything
// the user reads is in one place and in one language.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// kindText names what a unit is recorded as: the kinds of the glossary.
var kindText = map[model.UnitKind]string{
	model.KindExpense:        "支出",
	model.KindExpenseReward:  "支出＋回饋",
	model.KindIncome:         "收入",
	model.KindReward:         "回饋",
	model.KindDealing:        "往來增減",
	model.KindTransfer:       "轉帳",
	model.KindExchange:       "換匯",
	model.KindTrade:          "買賣",
	model.KindOpening:        "期初餘額",
	model.KindBillInstalment: "分期",
}

// kindOrder is the order kinds are listed in.
var kindOrder = []model.UnitKind{
	model.KindExpense, model.KindExpenseReward, model.KindIncome, model.KindReward, model.KindDealing,
	model.KindTransfer, model.KindExchange, model.KindTrade, model.KindOpening, model.KindBillInstalment,
}

// reasonInfo says why rows are not recorded and what to do about them.
type reasonInfo struct {
	Why      string
	Proposal string
}

var reasonText = map[model.Reason]reasonInfo{
	model.ReasonUnknownType: {
		"MOZE 的記錄類型，匯入程式不認得這一種",
		"先確認它是什麼；在那之前不匯入，餘額會與 CSV 差這幾列的影響",
	},
	model.ReasonBadSign: {
		"金額的正負號與 MOZE 的記錄類型不符",
		"確認原始資料；目前不匯入。若確定要記，在對照檔的 rows 以 treatment 指定",
	},
	model.ReasonZeroAmount: {
		"金額為 0 或折算後沒有金額，帳本不收",
		"不需處理；沒有金額就沒有餘額影響（手續費若有，列在影響欄）",
	},
	model.ReasonTooManyDecimals: {
		"小數位數超過標的的位數",
		"調高對照檔中該標的的 decimals 後再 plan",
	},
	model.ReasonZeroQuantity: {
		"買賣的非幣別標的，匯出的數量是 0，沒有數量就不能記成買賣",
		"預設不匯入：付款帳戶的餘額會比 CSV 多出付出的金額。查到數量後，在對照檔的 rows 填 quantity，就會記成買入／賣出",
	},
	model.ReasonNotCurrencies: {
		"兩邊都不是幣別，帳本沒有這種交易",
		"預設不匯入；要記就分開記成買賣",
	},
	model.ReasonUnpaired: {
		"找不到另一半的轉出／轉入",
		"預設不匯入；到 MOZE 補上另一半後重新匯出，或在帳本手動記",
	},
	model.ReasonNoPairing: {
		"同一分鐘的轉出／轉入，金額對不上任何一種配法",
		"預設不匯入；在帳本手動記，或到 MOZE 把金額改對",
	},
	model.ReasonAmbiguousPair: {
		"同一分鐘的轉出／轉入有不只一種配法，不猜",
		"預設不匯入；在帳本手動記，或把其中一筆在 MOZE 改一分鐘",
	},
	model.ReasonAmountMismatch: {
		"同一個標的，轉出與轉入的金額不同",
		"預設不匯入；差額可能是手續費，請到 MOZE 把手續費記在手續費欄",
	},
	model.ReasonSameAccount: {
		"轉出與轉入對到同一個帳戶（同標的）",
		"預設不匯入；若兩個 MOZE 帳戶不該合併，改對照檔的帳戶名稱",
	},
	model.ReasonInstalmentUnmatched: {
		"帳單分期／分期還款的列湊不成一個分期計畫",
		"預設不匯入；利息列已另記成支出。可在帳本手動記分期",
	},
	model.ReasonInstalmentAccounts: {
		"分期的列湊得起來，但在對照檔中是不同的帳戶，分期計畫只能在一個帳戶",
		"把這些 MOZE 帳戶在對照檔的 accounts 指到同一個帳戶名稱後再 plan，就會記成分期",
	},
	model.ReasonSkipped: {
		"對照檔的 rows 指定略過",
		"照你的指定",
	},
	model.ReasonNoDate: {
		"這列需要日期，檔案裡沒有",
		"預設不匯入",
	},
	model.ReasonNoParty: {
		"往來增減沒有對象",
		"在對照檔補上對象",
	},
	model.ReasonNoCategory: {
		"沒有類別",
		"在對照檔補上類別",
	},
}

// reasonDetail is the text of the arguments a reason has, for the column that says
// more of why.
func reasonDetail(reason model.Reason, args []string) string {
	at := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch reason {
	case model.ReasonUnknownType:
		return "類型：" + at(0)
	case model.ReasonTooManyDecimals, model.ReasonZeroQuantity:
		return "標的 " + strings.Join(args, "／")
	case model.ReasonNotCurrencies:
		return strings.Join(args, "／")
	case model.ReasonUnpaired, model.ReasonAmbiguousPair:
		return fmt.Sprintf("同一分鐘有 %s 筆轉出、%s 筆轉入", at(0), at(1))
	case model.ReasonSameAccount:
		return "帳本帳戶「" + at(0) + "」"
	case model.ReasonInstalmentAccounts:
		return "帳本帳戶：" + strings.Join(args, "、")
	}
	return ""
}

// pairingReasons are the reasons that are problems of pairing 轉出 and 轉入.
var pairingReasons = map[model.Reason]bool{
	model.ReasonUnpaired:       true,
	model.ReasonNoPairing:      true,
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
		"預設記成類別「收款（待確認）」的收入、貼標籤「待確認」；要改，在對照檔的 rows 用列的 key 指定（income、dealing、reward 或 skip）",
	},
	model.CaveatRefund: {
		"退款",
		"帳本沒有退款這個種類；預設記成類別「退款」的收入，原本的類別留在備註",
	},
	model.CaveatOpeningDate: {
		"期初餘額的日期不是由該帳戶自己的列決定",
		"帳戶沒有任何有日期的列，日期取同一個帳本帳戶（或整份檔案）最早一列的前一天；要指定日期，在對照檔的帳戶加 opening_date",
	},
}

// caveatDetail is the text of the arguments a caveat has.
func caveatDetail(kind model.CaveatKind, args []string) string {
	if kind == model.CaveatOpeningDate && len(args) > 0 {
		switch args[0] {
		case model.OpeningFromLedgerAccount:
			return "取同一個帳本帳戶最早一列的前一天"
		case model.OpeningFromFile:
			return "取整份檔案最早一列的前一天"
		}
	}
	return ""
}

// differenceText says why a unit is not recorded as its rows say, and what the
// ledger lacks of them.
var differenceText = map[model.DifferenceReason]string{
	model.DifferenceFee:      "手續費：帳本在這種單位上沒有地方記手續費",
	model.DifferenceDiscount: "折扣：帳本只有支出可以記折扣",
	model.DifferenceOther:    "原因不明，請回報這一組",
}

// sectionText names the sections of the mapping, as the user reads them in the file.
var sectionText = map[string]string{
	model.SectionInstruments: "標的",
	model.SectionAccounts:    "帳戶",
	model.SectionProjects:    "專案值",
	model.SectionExpense:     "支出類別",
	model.SectionIncome:      "收入類別",
	model.SectionParties:     "對象",
	model.SectionTags:        "標籤",
	model.SectionDefaults:    "預設類別",
	model.SectionRows:        "列的處理方式",
}

// problemText is what is wrong with the mapping or the rates, in words.
func problemText(p model.Problem) string {
	at := func(i int) string {
		if i < len(p.Args) {
			return p.Args[i]
		}
		return ""
	}
	var text string
	switch p.Code {
	case model.ProblemNameEmpty:
		text = "名稱是空的"
	case model.ProblemKindInvalid:
		text = fmt.Sprintf("種類「%s」不是這幾種之一：%s", at(0), strings.Join(kindsOf(p.Subject), "、"))
	case model.ProblemDecimalsRange:
		text = fmt.Sprintf("小數位數 %s 不在 0 到 18 之間", at(0))
	case model.ProblemDayRange:
		text = fmt.Sprintf("%s %s 不在 1 到 31 之間", at(0), at(1))
	case model.ProblemCardOnlyDays:
		text = "只有信用卡可以有結帳日與繳款日"
	case model.ProblemDueNeedsClosing:
		text = "有繳款日就要有結帳日"
	case model.ProblemAccountConflict:
		text = fmt.Sprintf("是帳本的帳戶「%s」，另一個項目把它寫成「%s」（機構「%s」）：種類與機構必須一致", at(0), at(1), at(2))
	case model.ProblemDealingsNeedsParty:
		text = "往來增減要有對象"
	case model.ProblemDefaultAreaUnknown:
		text = fmt.Sprintf("預設領域「%s」不是對照檔裡任何項目建立的領域", at(0))
	case model.ProblemOnlyProjectHasArea:
		text = "只有專案可以有預設領域"
	case model.ProblemTreatmentInvalid:
		text = fmt.Sprintf("處理方式「%s」不是這幾種之一：%s", at(0), strings.Join(treatmentsOf(p.Subject), "、"))
	case model.ProblemCategoryEmpty:
		text = "沒有類別"
	case model.ProblemOverrideNotFit:
		text = fmt.Sprintf("處理方式「%s」不適用於 MOZE 的記錄類型「%s」", at(0), at(1))
	case model.ProblemRateMissing:
		text = fmt.Sprintf("%s 在 %s 或更早沒有匯率", at(0), at(1))
	default:
		text = string(p.Code)
	}
	return p.Subject + "：" + text
}

// kindsOf are the kinds an entry can have, by the section its subject is in.
func kindsOf(subject string) []string {
	switch {
	case strings.HasPrefix(subject, model.SectionInstruments+"."):
		return model.InstrumentKinds
	case strings.HasPrefix(subject, model.SectionAccounts+"."):
		return model.AccountKinds
	}
	return []string{model.ProjectArea, model.ProjectProject, model.ProjectTag, model.ProjectDealings, model.ProjectNone}
}

// treatmentsOf are the treatments an entry can have, by the section its subject is in.
func treatmentsOf(subject string) []string {
	if strings.HasPrefix(subject, model.SectionRows+".") {
		return []string{model.TreatmentIncome, model.TreatmentExpense, model.TreatmentReward, model.TreatmentDealing, model.TreatmentSkip}
	}
	return []string{model.TreatmentReward, model.TreatmentDealing}
}

// warningText is what is odd, in words.
func warningText(w model.Warning) string {
	var text string
	switch w.Code {
	case model.WarningOverrideUnused:
		text = "這一列是分期計畫的一部分，不接受單獨指定處理方式，這個項目沒有作用"
	case model.WarningRowUnknown:
		text = "檔案裡沒有 key 是這個的列"
	case model.WarningActivePeriodIgnored:
		text = "`active_period` 已不再使用，已忽略（專案不設定進行期間）"
		if len(w.Args) > 0 {
			text += "；檔案第 " + w.Args[0] + " 行"
		}
	default:
		text = string(w.Code)
	}
	return w.Subject + "：" + text
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
