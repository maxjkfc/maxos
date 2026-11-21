package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// failureText says, in words, what stopped a command: a *model.Failure, whether it
// is the file adapters' or the use cases'. It tells when the error is not one, or is
// one with a code that has no text, so that the command goes on to show the error as
// it is.
//
// A failure is a code, where it is and the values the text of the code fills in (see
// model.Failure); the words are here, so that everything the user is told is in one
// language and says how to put it right. The words that are not ours are the ones of
// Err: the reply of the ledger and the reason the system gives, which are shown as
// they are.
func failureText(err error) (string, bool) {
	var f *model.Failure
	if !errors.As(err, &f) {
		return "", false
	}
	say, ok := failureTexts[f.Code]
	if !ok {
		return "", false
	}
	return place(f) + say(f), true
}

// place is where a failure is, with which its text begins: the file, and the line of it.
func place(f *model.Failure) string {
	switch {
	case f.File != "" && f.Line > 0:
		return fmt.Sprintf("%s 第 %d 行：", f.File, f.Line)
	case f.File != "":
		return f.File + "："
	case f.Line > 0:
		return fmt.Sprintf("第 %d 行：", f.Line)
	}
	return ""
}

// failureTexts says each code of failure, but for where it is, which place says. Each
// text names what is wrong, with the values the failure has, and what to do about it.
var failureTexts = map[model.FailureCode]func(f *model.Failure) string{
	// The files.
	model.FailureFileMissing: func(*model.Failure) string {
		return "找不到這個檔案；--file 要指到 MOZE 匯出的 CSV"
	},
	model.FailureFileUnreadable: func(f *model.Failure) string {
		return "讀不到這個檔案" + reasonOf(f) + "；確認它是一個檔案，而且你有讀取它的權限"
	},
	model.FailureFileNotWritten: func(f *model.Failure) string {
		return "寫不進這個檔案" + reasonOf(f) + "；確認它的資料夾存在、你有寫入的權限。什麼都沒有寫入"
	},
	model.FailureFileEmpty: func(*model.Failure) string {
		return "檔案是空的，連標題列都沒有"
	},

	// The CSV files.
	model.FailureColumnTwice: func(f *model.Failure) string {
		return fmt.Sprintf("標題列有兩個「%s」欄；一個欄位只能有一欄，把多的刪掉", arg(f, 0))
	},
	model.FailureColumnsMissing: func(f *model.Failure) string {
		return "標題列缺少欄位" + names(f.Args) + "；這些欄位名稱要一字不差地寫在標題列裡"
	},
	model.FailureFieldCount: func(f *model.Failure) string {
		return fmt.Sprintf("這一筆有 %s 個欄位，標題列有 %s 個；欄位數要一樣（內容裡有逗號或換行時，整個欄位要用雙引號包起來）", arg(f, 0), arg(f, 1))
	},
	model.FailureCSVBareQuote: func(f *model.Failure) string {
		return "沒有用雙引號包起來的欄位裡出現了雙引號；內容有雙引號時，整個欄位要包在雙引號裡，裡面的雙引號寫成兩個" + startedOn(f)
	},
	model.FailureCSVQuote: func(f *model.Failure) string {
		return "雙引號沒有成對：開了沒有關，或是關掉之後後面還有字" + startedOn(f)
	},
	model.FailureCSVSyntax: func(f *model.Failure) string {
		return "CSV 的格式有誤" + startedOn(f)
	},

	// The values of the files.
	model.FailureValueEmpty: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」是空的，這裡一定要有值", arg(f, 0))
	},
	model.FailureNotNumber: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是數字；只能有數字、小數點和正負號", arg(f, 0), arg(f, 1))
	},
	model.FailureNotWholeNumber: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是整數", arg(f, 0), arg(f, 1))
	},
	model.FailureNotBoolean: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是「true」或「false」", arg(f, 0), arg(f, 1))
	},
	model.FailureNotDay: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是日期，要寫成像「%s」這樣", arg(f, 0), arg(f, 1), arg(f, 2))
	},
	model.FailureNotTime: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是時間，要寫成像「15:04」這樣", arg(f, 0), arg(f, 1))
	},
	model.FailureRateNotPositive: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」要大於 0；它是一單位標的值多少 TWD", arg(f, 0), arg(f, 1))
	},
	model.FailureRateTwice: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」在「%s」已經有一個匯率了（第 %s 行）；一天只能有一個，留一個、刪掉另一個", arg(f, 0), arg(f, 1), arg(f, 2))
	},

	// The mapping file, as YAML.
	model.FailureYAMLSyntax: func(*model.Failure) string {
		return "不是合法的 YAML（括號、引號或縮排有誤）；改好才能讀"
	},
	model.FailureSecondDocument: func(*model.Failure) string {
		return "檔案裡有第二份 YAML 文件（用 --- 隔開的）；對照檔只能有一份，把後面的內容併進前面，或刪掉"
	},
	model.FailureNotSections: func(*model.Failure) string {
		return "對照檔最上層要是「instruments」、「accounts」這些區塊的對照，現在不是；整份檔案要寫成「區塊: 內容」"
	},
	model.FailureMustBeMapping: func(f *model.Failure) string {
		return whereText(arg(f, 0)) + "要是「鍵: 值」的對照（寫成大括號，或逐行縮排），現在不是"
	},
	model.FailureKeyNotSingle: func(f *model.Failure) string {
		return under(arg(f, 0)) + "有一個名稱不是單一的值（是清單或對照）；名稱只能是一個字或一串字"
	},
	model.FailureValueNotSingle: func(f *model.Failure) string {
		return fmt.Sprintf("「%s」的值不是單一的值（是清單或對照）；這裡只能寫一個值", arg(f, 0))
	},
	model.FailureKeyTwice: func(f *model.Failure) string {
		return fmt.Sprintf("%s有兩個「%s」（第一個在第 %s 行）；每個名稱只能有一個，刪掉或改名其中一個", under(arg(f, 0)), arg(f, 1), arg(f, 2))
	},
	model.FailureKeyUnknown: func(f *model.Failure) string {
		var known []string
		if len(f.Args) > 2 {
			known = f.Args[2:]
		}
		return fmt.Sprintf("%s有不認得的鍵「%s」；可以用的有：%s。是不是打錯了？", under(arg(f, 1)), arg(f, 0), strings.Join(quotedAll(known), "、"))
	},

	// What cannot be added to the mapping file.
	model.FailureEntryNotWritable: func(f *model.Failure) string {
		return fmt.Sprintf("%s裡的%s寫不成對照檔的一行（名稱不能換行、不能超過 128 個字，也要是合法的 UTF-8）。什麼都沒有寫入；"+
			"自己把這個項目寫進對照檔（名稱用雙引號包起來），或在 MOZE 裡改掉這個名稱", whereText(arg(f, 0)), quoted(arg(f, 1)))
	},
	model.FailureBraces: func(f *model.Failure) string {
		if arg(f, 0) == "" {
			return "整份對照檔寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成每個區塊各佔一行的寫法，或自己把缺的項目加進去"
		}
		return whereText(arg(f, 0)) + "寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成逐行縮排的寫法，或自己把缺的項目加進去"
	},
	model.FailureLayout: func(f *model.Failure) string {
		return whereText(arg(f, 0)) + "的寫法讓程式沒辦法補項目（空的大括號要和鍵寫在同一行）。什麼都沒有寫入；" +
			"把它改成鍵的後面直接逐行縮排，或自己把缺的項目加進去"
	},
	model.FailureWouldReadDifferently: func(f *model.Failure) string {
		if len(f.Args) < 2 {
			return "補上缺的項目之後，對照檔會變成讀不起來（例如檔案以文件結束標記收尾）。什麼都沒有寫入；改一下檔案的寫法，或自己把缺的項目加進去"
		}
		return fmt.Sprintf("補上缺的項目之後，%s裡的%s讀起來會和現在不同（例如多行的文字被截斷）。什麼都沒有寫入；"+
			"把它改成單行的寫法，或自己把缺的項目加進去", whereText(arg(f, 0)), quoted(arg(f, 1)))
	},

	// The ledger.
	model.FailureLedger: func(f *model.Failure) string {
		text := word(verbText, arg(f, 0), arg(f, 0)) + word(thingText, arg(f, 1), arg(f, 1))
		if name := arg(f, 2); name != "" {
			text += quoted(name)
		}
		text += "失敗"
		if f.Err != nil {
			text += "，帳本的回覆：" + f.Err.Error()
		}
		return text
	},
	model.FailureArchived: func(f *model.Failure) string {
		return fmt.Sprintf("帳本裡的%s%s已經封存，對照檔卻用到它；在帳本裡把它恢復使用，或讓對照檔改用別的名稱",
			word(thingText, arg(f, 0), arg(f, 0)), quoted(arg(f, 1)))
	},
	model.FailureInstrumentDiffers: func(f *model.Failure) string {
		return fmt.Sprintf("標的%s在帳本裡是%s，對照檔寫的是%s；兩邊要一致，改對照檔或改帳本都可以",
			quoted(arg(f, 0)), instrumentText(f.Args, 1), instrumentText(f.Args, 4))
	},
	model.FailureAccountKindDiffers: func(f *model.Failure) string {
		return fmt.Sprintf("帳戶%s在帳本裡的種類是%s，對照檔寫的是%s；兩邊要一致，改對照檔或改帳本都可以",
			quoted(arg(f, 0)), quoted(arg(f, 1)), quoted(arg(f, 2)))
	},
}

// verbText names what the import was doing to the ledger.
var verbText = map[model.Verb]string{
	model.VerbList:    "讀取",
	model.VerbGet:     "取得",
	model.VerbCreate:  "建立",
	model.VerbConfirm: "確認",
	model.VerbDiscard: "捨棄",
}

// thingText names what a step of the ledger was about, as the glossary does.
var thingText = map[model.Thing]string{
	model.ThingInstrument:  "標的",
	model.ThingInstitution: "機構",
	model.ThingAccount:     "帳戶",
	model.ThingCategory:    "類別",
	model.ThingArea:        "領域",
	model.ThingProject:     "專案",
	model.ThingTag:         "標籤",
	model.ThingParty:       "對象",
	model.ThingTransaction: "交易",
	model.ThingBalance:     "餘額",
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
	at := func(i int) string {
		if from+i < len(args) {
			return args[from+i]
		}
		return ""
	}
	currency := at(2)
	switch currency {
	case "currency":
		currency = "是幣別"
	case "not_currency":
		currency = "不是幣別"
	}
	return fmt.Sprintf("%s、%s 位小數、%s", quoted(at(0)), at(1), currency)
}

// arg is the value of the failure at the index, or nothing when it has none.
func arg(f *model.Failure, i int) string {
	if i < len(f.Args) {
		return f.Args[i]
	}
	return ""
}

// reasonOf is the reason the system gave, in its own words, as a remark; nothing when
// there is none.
func reasonOf(f *model.Failure) string {
	if f.Err == nil {
		return ""
	}
	return "（系統回報：" + f.Err.Error() + "）"
}

// startedOn says the line a record of a CSV file began on, when the failure has it:
// the line it is about is where its quote was found, which can be later.
func startedOn(f *model.Failure) string {
	if len(f.Args) == 0 {
		return ""
	}
	return "（這一筆從第 " + f.Args[0] + " 行開始）"
}

// sectionCategories is the section of the mapping that holds the expense and the
// income categories; the model names only those.
const sectionCategories = "categories"

// whereSections are the names of the sections in the where of a failure, the longest
// first, so that "categories.expense" is not read as an entry of "categories".
var whereSections = []string{
	model.SectionExpense, model.SectionIncome, model.SectionInstruments, model.SectionAccounts, model.SectionProjects,
	model.SectionParties, model.SectionTags, model.SectionDefaults, model.SectionRows, sectionCategories,
}

// whereText says where in the mapping file a failure is, from its where, which is
// written as the subject of a problem is: "accounts" is a section, "accounts.Name" an
// entry of it, and nothing is the top of the file.
func whereText(where string) string {
	if where == "" {
		return "最上層"
	}
	for _, section := range whereSections {
		if where == section {
			return sectionLabel(section)
		}
		if key, ok := strings.CutPrefix(where, section+"."); ok {
			return sectionLabel(section) + "裡的" + quoted(key)
		}
	}
	return quoted(where)
}

// under says what is below the where, so that a key in it can be said to be there.
func under(where string) string {
	if where == "" {
		return "最上層"
	}
	return whereText(where) + "底下"
}

// sectionLabel names a section as it is written in the file; the two of the categories
// are told as the part of categories they are.
func sectionLabel(section string) string {
	if group, name, ok := strings.Cut(section, "."); ok {
		return quoted(group) + "的" + quoted(name)
	}
	return quoted(section)
}

// names lists names, each in quotes.
func names(list []string) string { return strings.Join(quotedAll(list), "、") }

func quotedAll(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = quoted(s)
	}
	return out
}

// quoted puts a name in 「」. A name that has a line break or another character that
// cannot be shown is written the way Go does, in quotes, so that it can be told what it is.
func quoted(name string) string {
	if !utf8.ValidString(name) || strings.ContainsFunc(name, func(r rune) bool { return !unicode.IsPrint(r) }) {
		name = strconv.Quote(name)
	}
	return "「" + name + "」"
}
