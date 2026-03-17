package escrow

import "fmt"

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
const bech32Const = uint32(1)       // bech32 (witness v0)
const bech32mConst = uint32(0x2bc830a3) // bech32m (witness v1+)

func EncodeBech32m(hrp string, program []byte) (string, error) {
	conv, err := ConvertBits(program, 8, 5, true)
	if err != nil {
		return "", err
	}
	data := append([]byte{0x01}, conv...)
	return bech32mEncode(hrp, data)
}

func DecodeBech32(addr string) (witnessVersion byte, witnessProgram []byte, err error) {
	sepIdx := -1
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == '1' {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 {
		return 0, nil, fmt.Errorf("no separator found")
	}

	hrp := addr[:sepIdx]
	dataPart := addr[sepIdx+1:]
	if len(dataPart) < 6 {
		return 0, nil, fmt.Errorf("data too short")
	}

	data := make([]byte, len(dataPart))
	for i, c := range dataPart {
		idx := -1
		for j, ch := range bech32Charset {
			if byte(ch) == byte(c) {
				idx = j
				break
			}
		}
		if idx == -1 {
			return 0, nil, fmt.Errorf("invalid character %c", c)
		}
		data[i] = byte(idx)
	}

	// Verify checksum: bech32 (v0) uses constant 1, bech32m (v1+) uses 0x2bc830a3
	hrpExpand := make([]byte, 0, len(hrp)*2+1+len(data))
	for _, c := range hrp {
		hrpExpand = append(hrpExpand, byte(c>>5))
	}
	hrpExpand = append(hrpExpand, 0)
	for _, c := range hrp {
		hrpExpand = append(hrpExpand, byte(c&31))
	}
	values := append(hrpExpand, data...)
	polymod := bech32mPolymod(values)
	if polymod != bech32Const && polymod != bech32mConst {
		return 0, nil, fmt.Errorf("invalid bech32/bech32m checksum")
	}

	// Strip checksum (last 6 values)
	data = data[:len(data)-6]
	if len(data) < 1 {
		return 0, nil, fmt.Errorf("empty data")
	}

	witnessVersion = data[0]
	witnessProgram, err = ConvertBits(data[1:], 5, 8, false)
	if err != nil {
		return 0, nil, err
	}
	return witnessVersion, witnessProgram, nil
}

func AddressToOutputScript(addr string) ([]byte, error) {
	ver, prog, err := DecodeBech32(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode address %s: %w", addr, err)
	}

	var opVersion byte
	if ver == 0 {
		opVersion = 0x00
	} else {
		opVersion = 0x50 + ver
	}

	out := make([]byte, 0, 2+len(prog))
	out = append(out, opVersion, byte(len(prog)))
	out = append(out, prog...)
	return out, nil
}

func ConvertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	maxv := uint32((1 << toBits) - 1)
	var ret []byte
	for _, val := range data {
		acc = (acc << fromBits) | uint32(val)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	return ret, nil
}

func bech32mPolymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32mEncode(hrp string, data []byte) (string, error) {
	// hrp expand
	values := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for _, c := range hrp {
		values = append(values, byte(c>>5))
	}
	values = append(values, 0)
	for _, c := range hrp {
		values = append(values, byte(c&31))
	}
	values = append(values, data...)
	values = append(values, 0, 0, 0, 0, 0, 0)

	polymod := bech32mPolymod(values) ^ bech32mConst
	checksum := make([]byte, 6)
	for i := 0; i < 6; i++ {
		checksum[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}

	combined := append(data, checksum...)
	ret := hrp + "1"
	for _, d := range combined {
		ret += string(bech32Charset[d])
	}
	return ret, nil
}
