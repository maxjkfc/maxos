package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// failureText says, in words, what stopped a command: a failure of a file that the
// import reads or writes (*filefailure.Failure, which the file adapters give) or of its
// dealings with the ledger (*usecase.LedgerFailure, which the use cases give). It tells
// when the error is neither, or is one with a code that has no text, so that the
// command goes on to show the error as it is.
//
// A failure is a code, where it is and the values the text of the code fills in; the
// words are here, so that everything the user is told is in one language and says how
// to put it right. The words that are not ours are the ones of Err: the reply of the
// ledger and the reason the system gives, which are shown as they are.
func failureText(err error) (string, bool) {
	var file *filefailure.Failure
	if errors.As(err, &file) {
		say, ok := fileFailureTexts[file.Code]
		if !ok {
			return "", false
		}
		return place(file) + say(file), true
	}
	var ledger *usecase.LedgerFailure
	if errors.As(err, &ledger) {
		say, ok := ledgerFailureTexts[ledger.Code]
		if !ok {
			return "", false
		}
		return say(ledger), true
	}
	return "", false
}

// place is where a failure of a file is, with which its text begins: the file, and the
// line of it.
func place(f *filefailure.Failure) string {
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

// fileFailureTexts says each code of failure of a file, but for where it is, which
// place says. Each text names what is wrong, with the values the failure has, and what
// to do about it.
var fileFailureTexts = map[filefailure.Code]func(f *filefailure.Failure) string{
	// The files.
	filefailure.Missing: func(*filefailure.Failure) string {
		return "找不到這個檔案；--file 要指到 MOZE 匯出的 CSV"
	},
	filefailure.Unreadable: func(f *filefailure.Failure) string {
		return "讀不到這個檔案" + reasonOf(f) + "；確認它是一個檔案，而且你有讀取它的權限"
	},
	filefailure.NotWritten: func(f *filefailure.Failure) string {
		return "寫不進這個檔案" + reasonOf(f) + "；確認它的資料夾存在、你有寫入的權限。什麼都沒有寫入"
	},
	filefailure.Empty: func(*filefailure.Failure) string {
		return "檔案是空的，連標題列都沒有"
	},

	// The CSV files.
	filefailure.ColumnTwice: func(f *filefailure.Failure) string {
		return fmt.Sprintf("標題列有兩個「%s」欄；一個欄位只能有一欄，把多的刪掉", arg(f.Args, 0))
	},
	filefailure.ColumnsMissing: func(f *filefailure.Failure) string {
		return "標題列缺少欄位" + names(f.Args) + "；這些欄位名稱要一字不差地寫在標題列裡"
	},
	filefailure.FieldCount: func(f *filefailure.Failure) string {
		return fmt.Sprintf("這一筆有 %s 個欄位，標題列有 %s 個；欄位數要一樣（內容裡有逗號或換行時，整個欄位要用雙引號包起來）", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.CSVBareQuote: func(f *filefailure.Failure) string {
		return "沒有用雙引號包起來的欄位裡出現了雙引號；內容有雙引號時，整個欄位要包在雙引號裡，裡面的雙引號寫成兩個" + startedOn(f)
	},
	filefailure.CSVQuote: func(f *filefailure.Failure) string {
		return "雙引號沒有成對：開了沒有關，或是關掉之後後面還有字" + startedOn(f)
	},
	filefailure.CSVSyntax: func(f *filefailure.Failure) string {
		return "CSV 的格式有誤" + startedOn(f)
	},

	// The values of the files.
	filefailure.ValueEmpty: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」是空的，這裡一定要有值", arg(f.Args, 0))
	},
	filefailure.NotNumber: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是數字；只能有數字、小數點和正負號", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.NotWholeNumber: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是整數", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.NotBoolean: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是「true」或「false」", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.NotDay: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是日期，要寫成像「%s」這樣", arg(f.Args, 0), arg(f.Args, 1), arg(f.Args, 2))
	},
	filefailure.NotTime: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」不是時間，要寫成像「15:04」這樣", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.RateNotPositive: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值「%s」要大於 0；它是一單位標的值多少 TWD", arg(f.Args, 0), arg(f.Args, 1))
	},
	filefailure.RateTwice: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」在「%s」已經有一個匯率了（第 %s 行）；一天只能有一個，留一個、刪掉另一個", arg(f.Args, 0), arg(f.Args, 1), arg(f.Args, 2))
	},

	// The mapping file, as YAML.
	filefailure.YAMLSyntax: func(*filefailure.Failure) string {
		return "不是合法的 YAML（括號、引號或縮排有誤）；改好才能讀"
	},
	filefailure.SecondDocument: func(*filefailure.Failure) string {
		return "檔案裡有第二份 YAML 文件（用 --- 隔開的）；對照檔只能有一份，把後面的內容併進前面，或刪掉"
	},
	filefailure.NotSections: func(*filefailure.Failure) string {
		return "對照檔最上層要是「instruments」、「accounts」這些區塊的對照，現在不是；整份檔案要寫成「區塊: 內容」"
	},
	filefailure.MustBeMapping: func(f *filefailure.Failure) string {
		return whereText(arg(f.Args, 0)) + "要是「鍵: 值」的對照（寫成大括號，或逐行縮排），現在不是"
	},
	filefailure.KeyNotSingle: func(f *filefailure.Failure) string {
		return under(arg(f.Args, 0)) + "有一個名稱不是單一的值（是清單或對照）；名稱只能是一個字或一串字"
	},
	filefailure.ValueNotSingle: func(f *filefailure.Failure) string {
		return fmt.Sprintf("「%s」的值不是單一的值（是清單或對照）；這裡只能寫一個值", arg(f.Args, 0))
	},
	filefailure.KeyTwice: func(f *filefailure.Failure) string {
		return fmt.Sprintf("%s有兩個「%s」（第一個在第 %s 行）；每個名稱只能有一個，刪掉或改名其中一個", under(arg(f.Args, 0)), arg(f.Args, 1), arg(f.Args, 2))
	},
	filefailure.KeyUnknown: func(f *filefailure.Failure) string {
		var known []string
		if len(f.Args) > 2 {
			known = f.Args[2:]
		}
		return fmt.Sprintf("%s有不認得的鍵「%s」；可以用的有：%s。是不是打錯了？", under(arg(f.Args, 1)), arg(f.Args, 0), strings.Join(quotedAll(known), "、"))
	},

	// What cannot be added to the mapping file.
	filefailure.EntryNotWritable: func(f *filefailure.Failure) string {
		return fmt.Sprintf("%s裡的%s寫不成對照檔的一行（名稱不能換行、不能超過 128 個字，也要是合法的 UTF-8）。什麼都沒有寫入；"+
			"自己把這個項目寫進對照檔（名稱用雙引號包起來），或在 MOZE 裡改掉這個名稱", whereText(arg(f.Args, 0)), quoted(arg(f.Args, 1)))
	},
	filefailure.Braces: func(f *filefailure.Failure) string {
		if arg(f.Args, 0) == "" {
			return "整份對照檔寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成每個區塊各佔一行的寫法，或自己把缺的項目加進去"
		}
		return whereText(arg(f.Args, 0)) + "寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成逐行縮排的寫法，或自己把缺的項目加進去"
	},
	filefailure.Layout: func(f *filefailure.Failure) string {
		return whereText(arg(f.Args, 0)) + "的寫法讓程式沒辦法補項目（空的大括號要和鍵寫在同一行）。什麼都沒有寫入；" +
			"把它改成鍵的後面直接逐行縮排，或自己把缺的項目加進去"
	},
	filefailure.WouldReadDifferently: func(f *filefailure.Failure) string {
		if len(f.Args) < 2 {
			return "補上缺的項目之後，對照檔會變成讀不起來（例如檔案以文件結束標記收尾）。什麼都沒有寫入；改一下檔案的寫法，或自己把缺的項目加進去"
		}
		return fmt.Sprintf("補上缺的項目之後，%s裡的%s讀起來會和現在不同（例如多行的文字被截斷）。什麼都沒有寫入；"+
			"把它改成單行的寫法，或自己把缺的項目加進去", whereText(arg(f.Args, 0)), quoted(arg(f.Args, 1)))
	},
}

// arg is the value at the index of the values of a failure, or nothing when it has none.
func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// reasonOf is the reason the system gave, in its own words, as a remark; nothing when
// there is none.
func reasonOf(f *filefailure.Failure) string {
	if f.Err == nil {
		return ""
	}
	return "（系統回報：" + f.Err.Error() + "）"
}

// startedOn says the line a record of a CSV file began on, when the failure has it:
// the line it is about is where its quote was found, which can be later.
func startedOn(f *filefailure.Failure) string {
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
