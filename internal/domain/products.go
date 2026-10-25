package domain

import (
	"math"
	"sort"
)

// Product — кредитный продукт банка. Каталог хранится в коде и версионируется вместе с ним:
// в MVP это проще и прозрачнее, чем отдельный справочник в БД (см. docs/decisions.md).
type Product struct {
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	Tagline          string   `json:"tagline"`
	Description      string   `json:"description"`
	MinAmount        int64    `json:"min_amount"`
	MaxAmount        int64    `json:"max_amount"`
	MinTermMonths    int      `json:"min_term_months"`
	MaxTermMonths    int      `json:"max_term_months"`
	BaseRate         float64  `json:"base_rate"`          // % годовых для лучшего рейтинга
	AutoApproveLimit int64    `json:"auto_approve_limit"` // до этой суммы возможно автоодобрение; 0 — всегда ручное
	CollateralNote   string   `json:"collateral_note"`
	Features         []string `json:"features"`
}

// Products — каталог (синтетические условия, не публичная оферта банка).
var Products = []Product{
	{
		Code:             "EXPRESS",
		Name:             "Экспресс-кредит",
		Tagline:          "Быстрые деньги на текущие задачи",
		Description:      "Небольшая сумма без залога. Решение по большинству заявок принимается автоматически за несколько минут.",
		MinAmount:        100_000,
		MaxAmount:        5_000_000,
		MinTermMonths:    3,
		MaxTermMonths:    24,
		BaseRate:         23,
		AutoApproveLimit: 5_000_000,
		CollateralNote:   "Без залога",
		Features:         []string{"Без залога", "Автоматическое решение", "Минимум данных"},
	},
	{
		Code:             "WORKING_CAPITAL",
		Name:             "Оборотный кредит",
		Tagline:          "Закупка товара, сезонность, расчёты с поставщиками",
		Description:      "Пополнение оборотных средств на срок до 3 лет. Крупные суммы рассматривает специалист.",
		MinAmount:        500_000,
		MaxAmount:        30_000_000,
		MinTermMonths:    6,
		MaxTermMonths:    36,
		BaseRate:         19,
		AutoApproveLimit: 10_000_000,
		CollateralNote:   "Залог или поручительство — по желанию, улучшают условия",
		Features:         []string{"До 30 млн ₽", "До 36 месяцев", "Залог улучшает ставку"},
	},
	{
		Code:             "INVESTMENT",
		Name:             "Инвестиционный кредит",
		Tagline:          "Оборудование, транспорт, новое направление",
		Description:      "Долгосрочное финансирование развития бизнеса. Всегда рассматривается специалистом.",
		MinAmount:        1_000_000,
		MaxAmount:        100_000_000,
		MinTermMonths:    12,
		MaxTermMonths:    84,
		BaseRate:         17,
		AutoApproveLimit: 0,
		CollateralNote:   "Обычно требуется залог (приобретаемое имущество)",
		Features:         []string{"До 100 млн ₽", "До 7 лет", "Самая низкая ставка"},
	},
}

// ProductByCode ищет продукт в каталоге.
func ProductByCode(code string) (Product, bool) {
	for _, p := range Products {
		if p.Code == code {
			return p, true
		}
	}
	return Product{}, false
}

// Purpose — задача бизнеса, с которой клиент приходит за финансированием (JTBD).
// Клиент выбирает задачу, а не продукт — продукт подбирается системой.
type Purpose struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	Hint        string   `json:"hint"`
	Products    []string `json:"products"` // продукты в порядке приоритета
	Explanation string   `json:"explanation"`
}

var Purposes = []Purpose{
	{"EQUIPMENT", "Купить оборудование или транспорт", "Станок, автомобиль, техника для производства",
		[]string{"INVESTMENT", "EXPRESS"}, "Покупка, которая работает на бизнес годами, — лучше брать на долгий срок, чтобы платёж был комфортным."},
	{"WORKING_CAPITAL", "Закупить товар или сырьё", "Пополнить оборотные средства, рассчитаться с поставщиками",
		[]string{"WORKING_CAPITAL", "EXPRESS"}, "Деньги вернутся с продажи товара — подойдёт кредит на срок оборачиваемости."},
	{"CASH_GAP", "Закрыть кассовый разрыв", "Клиенты платят позже, а зарплату и аренду платить сейчас",
		[]string{"EXPRESS", "WORKING_CAPITAL"}, "Нужно быстро и ненадолго — важнее скорость решения, чем минимальная ставка."},
	{"SEASONAL", "Подготовиться к сезону", "Закупка заранее под сезонный спрос",
		[]string{"WORKING_CAPITAL", "EXPRESS"}, "Закупка под сезон окупается за несколько месяцев — подойдёт оборотный кредит."},
	{"NEW_DIRECTION", "Запустить новое направление", "Новая точка, продукт или услуга",
		[]string{"INVESTMENT", "WORKING_CAPITAL"}, "Развитие окупается дольше — нужен длинный срок и запас по платежу."},
	{"OTHER", "Другая задача", "Опишите своими словами",
		[]string{"WORKING_CAPITAL", "EXPRESS", "INVESTMENT"}, "Подберём продукт по сумме и сроку."},
}

// PurposeByCode ищет задачу.
func PurposeByCode(code string) (Purpose, bool) {
	for _, p := range Purposes {
		if p.Code == code {
			return p, true
		}
	}
	return Purpose{}, false
}

// Recommendation — продукт, подобранный под задачу, с пояснением.
type Recommendation struct {
	Product     Product `json:"product"`
	Recommended bool    `json:"recommended"`
	Fits        bool    `json:"fits"`
	Reason      string  `json:"reason"`
}

// Recommend подбирает продукты под задачу и примерную сумму.
// Порядок: сначала подходящие по сумме, внутри — по приоритету задачи.
func Recommend(purposeCode string, amount int64) []Recommendation {
	purpose, ok := PurposeByCode(purposeCode)
	if !ok {
		purpose, _ = PurposeByCode("OTHER")
	}
	priority := map[string]int{}
	for i, c := range purpose.Products {
		priority[c] = i
	}
	var out []Recommendation
	for _, p := range Products {
		_, inPurpose := priority[p.Code]
		r := Recommendation{Product: p, Fits: true}
		switch {
		case amount > 0 && amount < p.MinAmount:
			r.Fits = false
			r.Reason = "Сумма меньше минимальной для продукта (" + formatRub(p.MinAmount) + ")"
		case amount > p.MaxAmount:
			r.Fits = false
			r.Reason = "Сумма больше максимальной для продукта (" + formatRub(p.MaxAmount) + ")"
		case inPurpose:
			r.Reason = purpose.Explanation
		default:
			r.Reason = "Подходит по сумме, но обычно используется для других задач"
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Fits != out[j].Fits {
			return out[i].Fits
		}
		ri, rj := rankOf(priority, out[i].Product.Code), rankOf(priority, out[j].Product.Code)
		return ri < rj
	})
	for i := range out {
		if out[i].Fits {
			out[i].Recommended = true
			break
		}
	}
	return out
}

func rankOf(priority map[string]int, code string) int {
	if r, ok := priority[code]; ok {
		return r
	}
	return 100
}

// AnnuityPayment — ежемесячный аннуитетный платёж (в рублях, округление вверх).
func AnnuityPayment(amount int64, ratePercent float64, months int) int64 {
	if months <= 0 || amount <= 0 {
		return 0
	}
	r := ratePercent / 100 / 12
	if r == 0 {
		return int64(math.Ceil(float64(amount) / float64(months)))
	}
	p := float64(amount) * r / (1 - math.Pow(1+r, -float64(months)))
	return int64(math.Ceil(p))
}

// MaxAmountForPayment — максимальная сумма кредита, при которой платёж не превышает payment.
func MaxAmountForPayment(payment int64, ratePercent float64, months int) int64 {
	if payment <= 0 || months <= 0 {
		return 0
	}
	r := ratePercent / 100 / 12
	if r == 0 {
		return payment * int64(months)
	}
	pv := float64(payment) * (1 - math.Pow(1+r, -float64(months))) / r
	return int64(math.Floor(pv))
}

func formatRub(v int64) string {
	s := []byte{}
	str := []byte(itoa(v))
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			s = append(s, ' ')
		}
		s = append(s, c)
	}
	return string(s) + " ₽"
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// FormatRub — публичная версия форматирования суммы.
func FormatRub(v int64) string { return formatRub(v) }
