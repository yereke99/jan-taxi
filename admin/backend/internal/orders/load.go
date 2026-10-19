package orders

// OrderLoad is what an order asks for (ARCHITECTURE §21): the vehicle class
// with its localized name, the passenger count of a taxi order (null
// otherwise) and the cargo size and volume of a parcel/other order (null and
// 0 for taxi). Every order view embeds it.
type OrderLoad struct {
	VehicleClass     string        `json:"vehicle_class"`
	VehicleClassName LocalizedText `json:"vehicle_class_name"`
	PassengerCount   *int          `json:"passenger_count"`
	CargoSize        *string       `json:"cargo_size"`
	CargoVolumeL     int           `json:"cargo_volume_l"`
}

// loadColumns select the load of order o; loadJoin must be part of the FROM.
const loadColumns = `o.vehicle_class, ovc.name_kk, ovc.name_ru, ovc.name_en, o.passenger_count, o.cargo_size,
	o.cargo_volume_l`

const loadJoin = `
	JOIN vehicle_classes ovc ON ovc.code = o.vehicle_class`

// loadDest returns the scan targets of loadColumns.
func loadDest(l *OrderLoad) []any {
	return []any{&l.VehicleClass, &l.VehicleClassName.KK, &l.VehicleClassName.RU, &l.VehicleClassName.EN,
		&l.PassengerCount, &l.CargoSize, &l.CargoVolumeL}
}

// OrderPlace says where an order is (shared contract of every order DTO):
// the real settlement of pickup A from reverse geocoding (nil when unknown,
// never coordinates) and the city or nationwide region whose tariff priced
// the order.
type OrderPlace struct {
	Locality     *string      `json:"locality"`
	TariffRegion TariffRegion `json:"tariff_region"`
}

// TariffRegion is a short region reference (openapi TariffRegion).
type TariffRegion struct {
	Code string        `json:"code"`
	Name LocalizedText `json:"name"`
}

// placeColumns select the place of order o; placeJoin must be part of the FROM.
const placeColumns = `o.locality, trc.code, trc.name_kk, trc.name_ru, trc.name_en`

const placeJoin = `
	JOIN cities trc ON trc.id = o.city_id`

// placeDest returns the scan targets of placeColumns.
func placeDest(p *OrderPlace) []any {
	return []any{&p.Locality, &p.TariffRegion.Code, &p.TariffRegion.Name.KK, &p.TariffRegion.Name.RU,
		&p.TariffRegion.Name.EN}
}
