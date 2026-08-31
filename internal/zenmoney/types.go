package zenmoney

type DiffRequest struct {
	CurrentClientTimestamp int64         `json:"currentClientTimestamp"`
	ServerTimestamp        int64         `json:"serverTimestamp"`
	ForceFetch             []string      `json:"forceFetch,omitempty"`
	Instrument             []Instrument  `json:"instrument,omitempty"`
	Company                []Company     `json:"company,omitempty"`
	User                   []User        `json:"user,omitempty"`
	Account                []Account     `json:"account,omitempty"`
	Tag                    []Tag         `json:"tag,omitempty"`
	Merchant               []Merchant    `json:"merchant,omitempty"`
	Budget                 []any         `json:"budget,omitempty"`
	Reminder               []any         `json:"reminder,omitempty"`
	ReminderMarker         []any         `json:"reminderMarker,omitempty"`
	Transaction            []Transaction `json:"transaction,omitempty"`
	Deletion               []Deletion    `json:"deletion,omitempty"`
}

type DiffResponse struct {
	ServerTimestamp int64         `json:"serverTimestamp"`
	Instrument      []Instrument  `json:"instrument"`
	Company         []Company     `json:"company"`
	User            []User        `json:"user"`
	Account         []Account     `json:"account"`
	Tag             []Tag         `json:"tag"`
	Merchant        []Merchant    `json:"merchant"`
	Budget          []any         `json:"budget"`
	Reminder        []any         `json:"reminder"`
	ReminderMarker  []any         `json:"reminderMarker"`
	Transaction     []Transaction `json:"transaction"`
	Deletion        []Deletion    `json:"deletion"`
}

type Instrument struct {
	ID         int     `json:"id"`
	Changed    int64   `json:"changed"`
	Title      string  `json:"title"`
	ShortTitle string  `json:"shortTitle"`
	Symbol     string  `json:"symbol"`
	Rate       float64 `json:"rate"`
}

type User struct {
	ID          int    `json:"id"`
	Changed     int64  `json:"changed"`
	Login       string `json:"login"`
	Currency    int    `json:"currency"`
	Parent      *int   `json:"parent"`
	Country     int    `json:"country"`
	CountryCode string `json:"countryCode"`
	Email       string `json:"email"`
}

type Account struct {
	ID               string    `json:"id"`
	Changed          int64     `json:"changed"`
	User             int       `json:"user"`
	Instrument       *int      `json:"instrument"`
	Company          *int      `json:"company"`
	Type             string    `json:"type"`
	Title            string    `json:"title"`
	SyncID           *[]string `json:"syncID"`
	Balance          *float64  `json:"balance"`
	StartBalance     *float64  `json:"startBalance"`
	CreditLimit      *float64  `json:"creditLimit"`
	InBalance        bool      `json:"inBalance"`
	Savings          *bool     `json:"savings"`
	EnableCorrection bool      `json:"enableCorrection"`
	EnableSMS        bool      `json:"enableSMS"`
	Archive          bool      `json:"archive"`
	Private          bool      `json:"private"`
}

type Tag struct {
	ID            string  `json:"id"`
	Changed       int64   `json:"changed"`
	User          int     `json:"user"`
	Title         string  `json:"title"`
	Parent        *string `json:"parent"`
	Icon          *string `json:"icon"`
	Picture       *string `json:"picture"`
	Color         *int    `json:"color"`
	ShowIncome    bool    `json:"showIncome"`
	ShowOutcome   bool    `json:"showOutcome"`
	BudgetIncome  bool    `json:"budgetIncome"`
	BudgetOutcome bool    `json:"budgetOutcome"`
	Required      *bool   `json:"required"`
}

type Merchant struct {
	ID      string `json:"id"`
	Changed int64  `json:"changed"`
	User    int    `json:"user"`
	Title   string `json:"title"`
}

type Company struct {
	ID        int     `json:"id"`
	Changed   int64   `json:"changed"`
	Title     string  `json:"title"`
	Country   *int    `json:"country"`
	FullTitle *string `json:"fullTitle"`
	WWW       *string `json:"www"`
}

type Transaction struct {
	ID                  string   `json:"id"`
	Changed             int64    `json:"changed"`
	Created             int64    `json:"created"`
	User                int      `json:"user"`
	Deleted             bool     `json:"deleted"`
	Hold                *bool    `json:"hold"`
	Viewed              bool     `json:"viewed"`
	IncomeInstrument    int      `json:"incomeInstrument"`
	IncomeAccount       string   `json:"incomeAccount"`
	Income              float64  `json:"income"`
	IncomeBankID        *string  `json:"incomeBankID"`
	OutcomeInstrument   int      `json:"outcomeInstrument"`
	OutcomeAccount      string   `json:"outcomeAccount"`
	Outcome             float64  `json:"outcome"`
	OutcomeBankID       *string  `json:"outcomeBankID"`
	OpIncome            *float64 `json:"opIncome"`
	OpIncomeInstrument  *int     `json:"opIncomeInstrument"`
	OpOutcome           *float64 `json:"opOutcome"`
	OpOutcomeInstrument *int     `json:"opOutcomeInstrument"`
	Tag                 []string `json:"tag"`
	Merchant            *string  `json:"merchant"`
	Payee               *string  `json:"payee"`
	OriginalPayee       *string  `json:"originalPayee"`
	Comment             *string  `json:"comment"`
	Date                string   `json:"date"`
	MCC                 *int     `json:"mcc"`
	Latitude            *float64 `json:"latitude"`
	Longitude           *float64 `json:"longitude"`
	ReminderMarker      *string  `json:"reminderMarker"`
	QRCode              *string  `json:"qrCode"`
}

type Deletion struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type SuggestRequest struct {
	Payee    string `json:"payee,omitempty"`
	Merchant string `json:"merchant,omitempty"`
}

type SuggestResponse struct {
	Tag      []string `json:"tag"`
	Merchant *string  `json:"merchant"`
	Payee    *string  `json:"payee"`
}
