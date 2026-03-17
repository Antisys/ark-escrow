package script

import (
	"fmt"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/txscript"
)

// RelativeLocktimeType represents a BIP68 relative locktime type.
type RelativeLocktimeType uint

const (
	LocktimeTypeSecond RelativeLocktimeType = iota
	LocktimeTypeBlock
)

// RelativeLocktime represents a BIP68 relative timelock value.
type RelativeLocktime struct {
	Type  RelativeLocktimeType
	Value uint32
}

const (
	sequenceLockTimeMask        = 0x0000ffff
	sequenceLockTimeTypeFlag    = 1 << 22
	sequenceLockTimeGranularity = 9
	secondsMod                  = 1 << sequenceLockTimeGranularity
	secondsMax                  = sequenceLockTimeMask << sequenceLockTimeGranularity
	sequenceLockTimeDisableFlag = 1 << 31
)

// BIP68Sequence converts a RelativeLocktime to a BIP68 sequence number.
func BIP68Sequence(locktime RelativeLocktime) (uint32, error) {
	value := locktime.Value
	isSeconds := locktime.Type == LocktimeTypeSecond
	if isSeconds {
		if value > secondsMax {
			return 0, fmt.Errorf("seconds too large, max is %d", secondsMax)
		}
		if value%secondsMod != 0 {
			return 0, fmt.Errorf("seconds must be a multiple of %d", secondsMod)
		}
	}

	return blockchain.LockTimeToSequence(isSeconds, value), nil
}

// BIP68DecodeSequenceFromBytes decodes a BIP68 sequence from script bytes.
func BIP68DecodeSequenceFromBytes(sequence []byte) (*RelativeLocktime, error) {
	scriptNumber, err := txscript.MakeScriptNum(sequence, true, len(sequence))
	if err != nil {
		return nil, err
	}

	if scriptNumber >= txscript.OP_1 && scriptNumber <= txscript.OP_16 {
		scriptNumber = scriptNumber - (txscript.OP_1 - 1)
	}

	asNumber := int64(scriptNumber)

	if asNumber&sequenceLockTimeDisableFlag != 0 {
		return nil, fmt.Errorf("sequence is disabled")
	}
	if asNumber&sequenceLockTimeTypeFlag != 0 {
		seconds := asNumber & sequenceLockTimeMask << sequenceLockTimeGranularity
		return &RelativeLocktime{Type: LocktimeTypeSecond, Value: uint32(seconds)}, nil
	}

	return &RelativeLocktime{Type: LocktimeTypeBlock, Value: uint32(asNumber)}, nil
}
