// Package mocks — симуляторы внешних источников: ЕГРЮЛ/ЕГРИП, БКИ и «Прозрачный бизнес» ФНС.
// Все данные синтетические. Для демо-ИНН заданы профили, ведущие к разным исходам заявки;
// для любого другого корректного ИНН профиль генерируется детерминированно из хэша ИНН.
package mocks

import (
	"fmt"
	"hash/fnv"
	"time"

	"credit-conveyor/internal/integrations"
)

// Profile — согласованный набор данных по одному субъекту во всех источниках.
type Profile struct {
	EGRUL integrations.EGRULCompany
	BKI   integrations.BKIReport
	FNS   integrations.FNSInfo
	// BKITimeout — бюро «зависает» (для демонстрации отказоустойчивости).
	BKITimeout bool
}

// Демо-ИНН (синтетические, с корректными контрольными разрядами).
const (
	INNRomashka = "7709012344"   // хороший заёмщик → автоодобрение
	INNVector   = "7709023459"   // хороший, но просит слишком много → контрпредложение
	INNSeverov  = "771405678946" // ИП, были просрочки → ручной разбор
	INNGorizont = "7711034560"   // текущая просрочка 120 дней → отказ (стоп-фактор)
	INNTuman    = "7712045678"   // БКИ не отвечает → ручной разбор с пометкой
	INNLadoga   = "7713056785"   // свободная компания для онбординга нового клиента
)

var demoProfiles = map[string]Profile{
	INNRomashka: {
		EGRUL: integrations.EGRULCompany{
			INN: INNRomashka, OGRN: "1197746123456", KPP: "770901001",
			Name: "ООО «Ромашка»", FullName: "Общество с ограниченной ответственностью «Ромашка»", LegalForm: "ООО",
			Status: "ACTIVE", RegistrationDate: "2019-03-12",
			Director: integrations.Person{Name: "Иванова Анна Сергеевна", Position: "Генеральный директор", INN: "770912345678"},
			Founders: []integrations.Founder{{Name: "Иванова Анна Сергеевна", Share: 100}},
			OKVED:    integrations.OKVED{Code: "56.10", Name: "Деятельность ресторанов и услуги по доставке продуктов питания"},
			Address:  "г. Москва, ул. Садовая, д. 12, пом. 3", AuthorizedCapital: 100_000,
		},
		BKI: integrations.BKIReport{
			INN: INNRomashka, CreditHistoryMonths: 62, ClosedLoansCount: 4, Inquiries30D: 1,
			ActiveLoans: []integrations.BKILoan{
				{Creditor: "Банк «Северный»", Type: "Оборотный кредит", OpenedAt: "2025-02-10", Amount: 3_000_000, Balance: 1_800_000, MonthlyPayment: 95_000},
			},
		},
		FNS: integrations.FNSInfo{INN: INNRomashka, ReportingYear: 2025, ReportedRevenueLastYear: 58_000_000, EmployeesCount: 34, TaxRegime: "УСН"},
	},
	INNVector: {
		EGRUL: integrations.EGRULCompany{
			INN: INNVector, OGRN: "1207700234567", KPP: "770901001",
			Name: "ООО «Вектор»", FullName: "Общество с ограниченной ответственностью «Вектор»", LegalForm: "ООО",
			Status: "ACTIVE", RegistrationDate: "2020-01-20",
			Director: integrations.Person{Name: "Кузнецов Дмитрий Олегович", Position: "Генеральный директор", INN: "770923456789"},
			Founders: []integrations.Founder{{Name: "Кузнецов Дмитрий Олегович", Share: 60}, {Name: "Орлова Мария Викторовна", Share: 40}},
			OKVED:    integrations.OKVED{Code: "46.73", Name: "Торговля оптовая лесоматериалами, строительными материалами"},
			Address:  "г. Москва, Варшавское ш., д. 118, офис 405", AuthorizedCapital: 10_000,
		},
		BKI: integrations.BKIReport{
			INN: INNVector, CreditHistoryMonths: 50, ClosedLoansCount: 2, Inquiries30D: 1,
			ActiveLoans: []integrations.BKILoan{
				{Creditor: "Банк «Восток»", Type: "Лизинг автомобиля", OpenedAt: "2024-06-01", Amount: 2_100_000, Balance: 900_000, MonthlyPayment: 50_000},
			},
		},
		FNS: integrations.FNSInfo{INN: INNVector, ReportingYear: 2025, ReportedRevenueLastYear: 11_500_000, EmployeesCount: 9, TaxRegime: "ОСН"},
	},
	INNSeverov: {
		EGRUL: integrations.EGRULCompany{
			INN: INNSeverov, OGRN: "323774600012345",
			Name: "ИП Северов И. П.", FullName: "Индивидуальный предприниматель Северов Игорь Павлович", LegalForm: "ИП",
			Status: "ACTIVE", RegistrationDate: "2023-08-01",
			Director: integrations.Person{Name: "Северов Игорь Павлович", Position: "Индивидуальный предприниматель", INN: INNSeverov},
			OKVED:    integrations.OKVED{Code: "49.41", Name: "Деятельность автомобильного грузового транспорта"},
			Address:  "Московская обл., г. Подольск, ул. Комсомольская, д. 5",
		},
		BKI: integrations.BKIReport{
			INN: INNSeverov, CreditHistoryMonths: 26, ClosedLoansCount: 1, Inquiries30D: 3, MaxOverdueDays12M: 25,
			ActiveLoans: []integrations.BKILoan{
				{Creditor: "Банк «Западный»", Type: "Автокредит (грузовой автомобиль)", OpenedAt: "2024-03-15", Amount: 3_200_000, Balance: 1_900_000, MonthlyPayment: 85_000},
				{Creditor: "МФО «Быстрые деньги»", Type: "Микрозайм", OpenedAt: "2026-05-02", Amount: 400_000, Balance: 250_000, MonthlyPayment: 35_000},
			},
		},
		FNS: integrations.FNSInfo{INN: INNSeverov, ReportingYear: 2025, ReportedRevenueLastYear: 8_500_000, EmployeesCount: 4, TaxRegime: "УСН"},
	},
	INNGorizont: {
		EGRUL: integrations.EGRULCompany{
			INN: INNGorizont, OGRN: "1177746345678", KPP: "771101001",
			Name: "ООО «Горизонт»", FullName: "Общество с ограниченной ответственностью «Горизонт»", LegalForm: "ООО",
			Status: "ACTIVE", RegistrationDate: "2017-05-30",
			Director: integrations.Person{Name: "Морозов Сергей Анатольевич", Position: "Генеральный директор", INN: "771134567890"},
			Founders: []integrations.Founder{{Name: "Морозов Сергей Анатольевич", Share: 100}},
			OKVED:    integrations.OKVED{Code: "41.20", Name: "Строительство жилых и нежилых зданий"},
			Address:  "г. Москва, ул. Промышленная, д. 7, стр. 2", AuthorizedCapital: 10_000,
		},
		BKI: integrations.BKIReport{
			INN: INNGorizont, CreditHistoryMonths: 84, ClosedLoansCount: 5, Inquiries30D: 6, MaxOverdueDays12M: 120,
			CurrentOverdueAmount: 2_400_000,
			ActiveLoans: []integrations.BKILoan{
				{Creditor: "Банк «Южный»", Type: "Кредитная линия", OpenedAt: "2023-09-01", Amount: 10_000_000, Balance: 7_600_000, MonthlyPayment: 420_000, CurrentOverdueDays: 120},
			},
		},
		FNS: integrations.FNSInfo{INN: INNGorizont, ReportingYear: 2025, ReportedRevenueLastYear: 31_000_000, EmployeesCount: 22, TaxRegime: "ОСН", TaxArrears: 1_400_000, MassAddress: true},
	},
	INNTuman: {
		EGRUL: integrations.EGRULCompany{
			INN: INNTuman, OGRN: "1217700456789", KPP: "771201001",
			Name: "ООО «Туман»", FullName: "Общество с ограниченной ответственностью «Туман»", LegalForm: "ООО",
			Status: "ACTIVE", RegistrationDate: "2021-09-14",
			Director: integrations.Person{Name: "Лебедева Ольга Игоревна", Position: "Генеральный директор", INN: "771245678901"},
			Founders: []integrations.Founder{{Name: "Лебедева Ольга Игоревна", Share: 100}},
			OKVED:    integrations.OKVED{Code: "62.01", Name: "Разработка компьютерного программного обеспечения"},
			Address:  "г. Москва, Ленинский пр-т, д. 30А, офис 12", AuthorizedCapital: 50_000,
		},
		BKI:        integrations.BKIReport{INN: INNTuman, CreditHistoryMonths: 30, Inquiries30D: 1},
		FNS:        integrations.FNSInfo{INN: INNTuman, ReportingYear: 2025, ReportedRevenueLastYear: 24_000_000, EmployeesCount: 15, TaxRegime: "УСН"},
		BKITimeout: true,
	},
	INNLadoga: {
		EGRUL: integrations.EGRULCompany{
			INN: INNLadoga, OGRN: "1187746567890", KPP: "771301001",
			Name: "ООО «Ладога»", FullName: "Общество с ограниченной ответственностью «Ладога»", LegalForm: "ООО",
			Status: "ACTIVE", RegistrationDate: "2018-11-02",
			Director: integrations.Person{Name: "Смирнов Алексей Викторович", Position: "Генеральный директор", INN: "771356789012"},
			Founders: []integrations.Founder{{Name: "Смирнов Алексей Викторович", Share: 70}, {Name: "Смирнова Елена Петровна", Share: 30}},
			OKVED:    integrations.OKVED{Code: "31.09", Name: "Производство прочей мебели"},
			Address:  "г. Санкт-Петербург, ул. Заводская, д. 15, лит. Б", AuthorizedCapital: 200_000,
		},
		BKI: integrations.BKIReport{
			INN: INNLadoga, CreditHistoryMonths: 70, ClosedLoansCount: 3, Inquiries30D: 0,
			ActiveLoans: []integrations.BKILoan{
				{Creditor: "Банк «Северный»", Type: "Инвестиционный кредит", OpenedAt: "2022-04-20", Amount: 6_000_000, Balance: 2_100_000, MonthlyPayment: 140_000},
			},
		},
		FNS: integrations.FNSInfo{INN: INNLadoga, ReportingYear: 2025, ReportedRevenueLastYear: 41_000_000, EmployeesCount: 27, TaxRegime: "ОСН"},
	},
}

var (
	names     = []string{"Альфа-Строй", "Берёзка", "Меридиан", "Профит", "Север-Логистик", "Техносфера", "Добрый хлеб", "Импульс", "Каскад", "Орбита"}
	directors = []string{"Попов Андрей Николаевич", "Васильева Екатерина Юрьевна", "Новиков Павел Сергеевич", "Фёдорова Наталья Ивановна", "Волков Михаил Андреевич"}
	okveds    = []integrations.OKVED{
		{Code: "47.19", Name: "Торговля розничная прочая в неспециализированных магазинах"},
		{Code: "43.21", Name: "Производство электромонтажных работ"},
		{Code: "10.71", Name: "Производство хлеба и мучных кондитерских изделий"},
		{Code: "52.29", Name: "Деятельность вспомогательная прочая, связанная с перевозками"},
		{Code: "62.02", Name: "Деятельность консультативная в области компьютерных технологий"},
	}
)

// LookupProfile возвращает профиль по ИНН. ok=false — субъект «не найден в реестре».
func LookupProfile(inn string) (Profile, bool) {
	if p, ok := demoProfiles[inn]; ok {
		return p, true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(inn))
	v := h.Sum64()
	if v%10 == 0 { // ~10% ИНН «не найдены» — чтобы показать обработку этого случая
		return Profile{}, false
	}
	pick := func(n int, shift uint) int { return int((v >> shift) % uint64(n)) }

	isIP := len(inn) == 12
	years := 1 + pick(12, 3)
	reg := time.Date(2026-years, time.Month(1+pick(12, 7)), 1+pick(27, 11), 0, 0, 0, 0, time.UTC)
	revenue := int64(3+pick(80, 15)) * 1_000_000
	name := names[pick(len(names), 19)]
	director := directors[pick(len(directors), 23)]
	okved := okveds[pick(len(okveds), 27)]
	legalForm, fullName, shortName := "ООО", "Общество с ограниченной ответственностью «"+name+"»", "ООО «"+name+"»"
	if isIP {
		legalForm, fullName, shortName = "ИП", "Индивидуальный предприниматель "+director, "ИП "+director
	}
	overdue := 0
	if pick(5, 31) == 0 {
		overdue = 10 + pick(40, 35)
	}
	monthly := int64(pick(6, 39)) * revenue / 12 / 20
	var loans []integrations.BKILoan
	if monthly > 0 {
		loans = append(loans, integrations.BKILoan{Creditor: "Банк «Центральный»", Type: "Кредит на развитие", OpenedAt: "2025-01-15",
			Amount: monthly * 30, Balance: monthly * 18, MonthlyPayment: monthly})
	}
	return Profile{
		EGRUL: integrations.EGRULCompany{
			INN: inn, OGRN: fmt.Sprintf("1%02d77%08d", reg.Year()%100, v%100_000_000), Name: shortName, FullName: fullName,
			LegalForm: legalForm, Status: "ACTIVE", RegistrationDate: reg.Format("2006-01-02"),
			Director: integrations.Person{Name: director, Position: "Генеральный директор"},
			Founders: []integrations.Founder{{Name: director, Share: 100}},
			OKVED:    okved, Address: "г. Москва, ул. Синтетическая, д. " + fmt.Sprint(1+pick(99, 43)),
			AuthorizedCapital: 10_000,
		},
		BKI: integrations.BKIReport{INN: inn, CreditHistoryMonths: years * 10 % 90, ClosedLoansCount: pick(4, 47),
			Inquiries30D: pick(4, 51), MaxOverdueDays12M: overdue, ActiveLoans: loans},
		FNS: integrations.FNSInfo{INN: inn, ReportingYear: 2025, ReportedRevenueLastYear: revenue, EmployeesCount: 2 + pick(60, 55), TaxRegime: "УСН"},
	}, true
}
