package charger

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/modbus"
	"github.com/evcc-io/evcc/util/sponsor"
)

type Enovates struct {
	log     *util.Logger
	conn    *modbus.Connection
	curr    uint16
	enabled bool
}

const (
	enovatesRegPhases         = 51  // Number of connector output phases
	enovatesRegMaxAmp         = 52  // Max Amp per phase
	enovatesRegOCPPStatus     = 53  // OCPP Status
	enovatesRegLoadShedding   = 54  // Load shedding enabled
	enovatesRegLockState      = 55  // Lock state
	enovatesRegContactor      = 56  // Contactor state
	enovatesRegLED            = 57  // LED Index
	enovatesRegCurrents       = 201 // Measured Current L1-L3
	enovatesRegVoltages       = 204 // Measured Voltage L1-L3
	enovatesRegPowerTotal     = 207 // Power Active Total
	enovatesRegEnergy         = 214 // Active Energy Import total
	enovatesRegStatus         = 301 // Mode 3 state numeric
	enovatesRegCurrentOffered = 401 // Current Offered
)

func init() {
	registry.Add("enovates", NewEnovatesFromConfig)
}

// NewEnovatesFromConfig creates an Enovates charger from generic config
func NewEnovatesFromConfig(other map[string]interface{}) (api.Charger, error) {
	cc := modbus.Settings{
		ID: 1,
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	return NewEnovates(cc.URI, cc.Device, cc.Comset, cc.Baudrate, cc.Protocol(), cc.ID)
}

// NewEnovates creates an Enovates charger
func NewEnovates(uri, device, comset string, baudrate int, proto modbus.Protocol, slaveID uint8) (api.Charger, error) {
	conn, err := modbus.NewConnection(uri, device, comset, baudrate, proto, slaveID)
	if err != nil {
		return nil, err
	}

	if !sponsor.IsAuthorized() {
		return nil, api.ErrSponsorRequired
	}

	log := util.NewLogger("enovates")
	conn.Logger(log.TRACE)

	wb := &Enovates{
		log:  log,
		conn: conn,
		curr: 6000, // Default minimum current (mA)
	}

	go wb.heartbeat()

	return wb, nil
}

func (wb *Enovates) heartbeat() {
	for range time.Tick(30 * time.Second) {
		_, _ = wb.status()
	}
}

func (wb *Enovates) status() (uint16, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegStatus, 1)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint16(b), nil
}

// Status implements the api.Charger interface
func (wb *Enovates) Status() (api.ChargeStatus, error) {
	s, err := wb.status()
	if err != nil {
		return api.StatusNone, err
	}

	switch s {
	case 0:
		return api.StatusA, nil
	case 1, 2, 3:
		return api.StatusB, nil
	case 4:
		return api.StatusC, nil
	default:
		return api.StatusNone, fmt.Errorf("invalid mode 3 state: %d", s)
	}
}

// Enabled implements the api.Charger interface
func (wb *Enovates) Enabled() (bool, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegCurrentOffered, 1)
	if err != nil {
		return false, err
	}

	return binary.BigEndian.Uint16(b) > 0, nil
}

// Enable implements the api.Charger interface
func (wb *Enovates) Enable(enable bool) error {
	var current uint16
	if enable {
		current = wb.curr
	}

	_, err := wb.conn.WriteSingleRegister(enovatesRegCurrentOffered, current)
	return err
}

// MaxCurrent implements the api.Charger interface
func (wb *Enovates) MaxCurrent(current int64) error {
	if current < 6 {
		return fmt.Errorf("invalid current %.1f", float64(current))
	}

	wb.curr = uint16(current * 1000)
	return wb.Enable(true)
}

// Currents provides measured currents for each phase
func (wb *Enovates) Currents() (float64, float64, float64, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegCurrents, 3)
	if err != nil {
		return 0, 0, 0, err
	}
	return float64(binary.BigEndian.Uint16(b[0:2])) / 1000,
		float64(binary.BigEndian.Uint16(b[2:4])) / 1000,
		float64(binary.BigEndian.Uint16(b[4:6])) / 1000, nil
}

// Voltages provides measured voltages for each phase
func (wb *Enovates) Voltages() (float64, float64, float64, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegVoltages, 3)
	if err != nil {
		return 0, 0, 0, err
	}
	return float64(binary.BigEndian.Uint16(b[0:2])) / 10,
		float64(binary.BigEndian.Uint16(b[2:4])) / 10,
		float64(binary.BigEndian.Uint16(b[4:6])) / 10, nil
}

// TotalEnergy returns the total energy delivered (in kWh)
func (wb *Enovates) TotalEnergy() (float64, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegEnergy, 2)
	if err != nil {
		return 0, err
	}
	return float64(binary.BigEndian.Uint32(b)) / 1000, nil
}

// MaxAmps returns the charger's maximum supported current
func (wb *Enovates) MaxAmps() (int, error) {
	b, err := wb.conn.ReadHoldingRegisters(enovatesRegMaxAmp, 1)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(b)), nil
}
