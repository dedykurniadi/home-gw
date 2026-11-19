// Paket sonoff mengontrol Sonoff eWeLink lewat LAN mode (HTTP terenkripsi AES-128-CBC, port 8081)
// dan membaca status dari pengumuman mDNS (_ewelink._tcp).
package sonoff

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// port HTTP LAN mode perangkat; variabel supaya bisa diganti di tes.
var port = "8081"

var httpClient = &http.Client{Timeout: 5 * time.Second}

var ErrUnknown = errors.New("perangkat tidak dikenal")

// ErrInvalid: permintaan pengaturan tidak valid atau tidak didukung perangkat ini.
var ErrInvalid = errors.New("pengaturan tidak valid")

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
	MAC  string `json:"mac"`
	IP   string `json:"ip,omitempty"`
	// Kind: "switch" (default, Sonoff Mini) atau "switches" (POWR3, uiid 190).
	Kind string `json:"kind,omitempty"`
	// Watt / StandbyW (opsional): daya beban saat ON dan saat OFF, untuk estimasi kWh (mis. lampu LED di Mini).
	Watt     float64 `json:"watt,omitempty"`
	StandbyW float64 `json:"standby_watt,omitempty"`
}

func newBlock(devicekey string) (cipher.Block, error) {
	k := md5.Sum([]byte(devicekey)) // kunci AES-128 = MD5(devicekey)
	return aes.NewCipher(k[:])
}

func encrypt(devicekey string, plain []byte) (ivB64, dataB64 string, err error) {
	blk, err := newBlock(devicekey)
	if err != nil {
		return "", "", err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", "", err
	}
	n := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(n)}, n)...)
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(enc, padded)
	return base64.StdEncoding.EncodeToString(iv), base64.StdEncoding.EncodeToString(enc), nil
}

func decrypt(devicekey, ivB64, dataB64 string) ([]byte, error) {
	blk, err := newBlock(devicekey)
	if err != nil {
		return nil, err
	}
	iv, err := base64.StdEncoding.DecodeString(ivB64)
	if err != nil || len(iv) != aes.BlockSize {
		return nil, errors.New("iv tidak valid")
	}
	ct, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil || len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("data tidak valid")
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, ct)
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize || n > len(out) {
		return nil, errors.New("padding tidak valid (key salah?)")
	}
	for _, b := range out[len(out)-n:] {
		if int(b) != n {
			return nil, errors.New("padding tidak valid (key salah?)")
		}
	}
	return out[:len(out)-n], nil
}

type request struct {
	Sequence   string `json:"sequence"`
	DeviceID   string `json:"deviceid"`
	SelfApikey string `json:"selfApikey"`
	IV         string `json:"iv"`
	Encrypt    bool   `json:"encrypt"`
	Data       string `json:"data"`
}

func (d Device) post(ip, path string, params any) error {
	plain, err := json.Marshal(params)
	if err != nil {
		return err
	}
	iv, data, err := encrypt(d.Key, plain)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(request{
		Sequence:   strconv.FormatInt(time.Now().UnixMilli(), 10),
		DeviceID:   d.ID,
		SelfApikey: "123",
		IV:         iv,
		Encrypt:    true,
		Data:       data,
	})
	resp, err := httpClient.Post("http://"+net.JoinHostPort(ip, port)+"/zeroconf/"+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var r struct {
		Error int `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("respons bukan JSON: %.80s", raw)
	}
	if r.Error != 0 {
		return fmt.Errorf("perangkat membalas error=%d", r.Error)
	}
	return nil
}

// SetSwitch menyalakan / mematikan relay perangkat di alamat ip.
func (d Device) SetSwitch(ip string, on bool) error {
	v := "off"
	if on {
		v = "on"
	}
	if d.Kind == "switches" {
		// Seperti kelas XSwitchPOWR3 di SonoffLAN: keluarga POWR3/uiid 190 menolak (error 400)
		// perintah tanpa operSide.
		return d.post(ip, "switches", map[string]any{"switches": []map[string]any{{"switch": v, "outlet": 0}}, "operSide": 1})
	}
	return d.post(ip, "switch", map[string]any{"switch": v})
}

// SetConfig mengirim satu perintah pengaturan Mini: path "startup", "pulse", atau "sledonline".
func (d Device) SetConfig(ip, path string, params map[string]any) error {
	return d.post(ip, path, params)
}
