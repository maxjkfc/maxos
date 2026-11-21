package cli

import (
	"fmt"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// ledgerFailureTexts says each code of failure of the dealings with the ledger. Each
// text names what is wrong, with the values the failure has, and what to do about it.
//
// The ledger cannot change an instrument it has, nor the kind of an account, nor bring
// back what is archived: instruments are created and archived, and an account is created,
// given the days of its statements and archived. So what is left to do about the three of
// them is to change the mapping, and their texts say that and nothing of changing the
// ledger.
var ledgerFailureTexts = map[usecase.LedgerFailureCode]func(f *usecase.LedgerFailure) string{
	usecase.LedgerStepFailed: func(f *usecase.LedgerFailure) string {
		text := word(verbText, arg(f.Args, 0), arg(f.Args, 0)) + word(thingText, arg(f.Args, 1), arg(f.Args, 1))
		if name := arg(f.Args, 2); name != "" {
			text += quoted(name)
		}
		text += "失敗"
		if f.Err != nil {
			text += "，帳本的回覆：" + f.Err.Error()
		}
		return text
	},
	usecase.LedgerArchived: func(f *usecase.LedgerFailure) string {
		return fmt.Sprintf("帳本裡的%s%s已經封存，對照檔卻用到它；改對照檔，用別的名稱（帳本裡已封存的不能恢復使用）",
			word(thingText, arg(f.Args, 0), arg(f.Args, 0)), quoted(arg(f.Args, 1)))
	},
	usecase.LedgerInstrumentDiffers: func(f *usecase.LedgerFailure) string {
		return fmt.Sprintf("標的%s在帳本裡是%s，對照檔寫的是%s；兩邊要一致，改對照檔（帳本裡已建立的標的與帳戶不能修改）",
			quoted(arg(f.Args, 0)), instrumentText(f.Args, 1), instrumentText(f.Args, 4))
	},
	usecase.LedgerAccountKindDiffers: func(f *usecase.LedgerFailure) string {
		return fmt.Sprintf("帳戶%s在帳本裡的種類是%s，對照檔寫的是%s；兩邊要一致，改對照檔（帳本裡已建立的標的與帳戶不能修改）",
			quoted(arg(f.Args, 0)), quoted(arg(f.Args, 1)), quoted(arg(f.Args, 2)))
	},
}

// verbText names what the import was doing to the ledger.
var verbText = map[usecase.Verb]string{
	usecase.VerbList:    "讀取",
	usecase.VerbGet:     "取得",
	usecase.VerbCreate:  "建立",
	usecase.VerbConfirm: "確認",
	usecase.VerbDiscard: "捨棄",
}

// thingText names what a step of the ledger was about, as the glossary does.
var thingText = map[usecase.Thing]string{
	usecase.ThingInstrument:  "標的",
	usecase.ThingInstitution: "機構",
	usecase.ThingAccount:     "帳戶",
	usecase.ThingCategory:    "類別",
	usecase.ThingArea:        "領域",
	usecase.ThingProject:     "專案",
	usecase.ThingTag:         "標籤",
	usecase.ThingParty:       "對象",
	usecase.ThingTransaction: "交易",
	usecase.ThingBalance:     "餘額",
}

// word is the text of a value that is a verb or a thing, or the value as it is when
// the text does not have it, so that a value the text does not know is still shown.
func word[K ~string](texts map[K]string, key, fallback string) string {
	if text, ok := texts[K(key)]; ok {
		return text
	}
	return fallback
}

// instrumentText says what an instrument is, from the three values at the index: its
// kind, its decimals and whether it is a currency.
func instrumentText(args []string, from int) string {
	at := func(i int) string { return arg(args, from+i) }
	currency := at(2)
	switch currency {
	case "currency":
		currency = "是幣別"
	case "not_currency":
		currency = "不是幣別"
	}
	return fmt.Sprintf("%s、%s 位小數、%s", quoted(at(0)), at(1), currency)
}
