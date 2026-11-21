package web

import "github.com/maxjkfc/maxos/app/finance/domain/model"

// What the pages call things: the words of CONTEXT.md, never the ones it says to
// avoid. A value that is not known is shown as it is, which is a reminder to add
// it here and not a page that fails.

// option is a value of a filter and what it is called.
type option struct {
	Value string
	Label string
}

// kindOptions are the kinds of a transaction, in the order the glossary names them,
// with the opening balance, which the glossary does not, last. They are what the
// filter of the list offers.
var kindOptions = []option{
	{string(model.KindExpense), "支出"},
	{string(model.KindIncome), "收入"},
	{string(model.KindTransfer), "轉帳"},
	{string(model.KindExchange), "換匯"},
	{string(model.KindTrade), "買賣"},
	{string(model.KindReward), "回饋"},
	{string(model.KindDealing), "往來增減"},
	{string(model.KindInstalment), "分期"},
	{string(model.KindReconciliation), "對帳"},
	{string(model.KindOpening), "期初餘額"},
}

// statusOptions are the statuses of a transaction.
var statusOptions = []option{
	{string(model.StatusDraft), "草稿"},
	{string(model.StatusConfirmed), "已確認"},
	{string(model.StatusVoid), "已作廢"},
}

func labelOf(options []option, value string) string {
	for _, o := range options {
		if o.Value == value {
			return o.Label
		}
	}
	return value
}

func kindLabel(kind string) string     { return labelOf(kindOptions, kind) }
func statusLabel(status string) string { return labelOf(statusOptions, status) }

// accountKindLabel is what an account of that kind is.
func accountKindLabel(kind string) string {
	switch model.AccountKind(kind) {
	case model.AccountKindCash:
		return "現金"
	case model.AccountKindBank:
		return "銀行戶頭"
	case model.AccountKindCard:
		return "信用卡"
	case model.AccountKindExchange:
		return "交易所"
	case model.AccountKindBrokerage:
		return "投資帳戶"
	case model.AccountKindWallet:
		return "錢包"
	case model.AccountKindLoan:
		return "貸款"
	default:
		return kind
	}
}
