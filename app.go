/*******************************************************************************
*   (c) 2019 ZondaX GmbH
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
********************************************************************************/

package ledger_oasis_go

import (
	"fmt"
	"math"

	"github.com/cosmos/ledger-go"
)

const (
	CLA = 0x05

	INSGetVersion     = 0
	INSGetAddrEd25519 = 1
	INSSignEd25519    = 2

	userMessageChunkSize = 250
)

const (
	PayloadChunkInit = 0
	PayloadChunkAdd  = 1
	PayloadChunkLast = 2
)

// LedgerOasis represents a connection to the Ledger app
type LedgerOasis struct {
	api     *ledger_go.Ledger
	version VersionInfo
}

// FindLedgerOasisApp finds the Oasis app running in a Ledger device
func FindLedgerOasisApp() (*LedgerOasis, error) {
	ledgerAPI, err := ledger_go.FindLedger()

	if err != nil {
		return nil, err
	}

	app := LedgerOasis{ledgerAPI, VersionInfo{}}
	appVersion, err := app.GetVersion()

	if err != nil {
		defer ledgerAPI.Close()
		if err.Error() == "[APDU_CODE_CLA_NOT_SUPPORTED] Class not supported" {
			return nil, fmt.Errorf("are you sure the Oasis app is open?")
		}
		return nil, err
	}

	err = app.CheckVersion(*appVersion)
	if err != nil {
		defer ledgerAPI.Close()
		return nil, err
	}

	return &app, err
}

// Close closes a connection with the Oasis user app
func (ledger *LedgerOasis) Close() error {
	return ledger.api.Close()
}

// VersionIsSupported returns true if the App version is supported by this library
func (ledger *LedgerOasis) CheckVersion(ver VersionInfo) error {
	return CheckVersion(ver, VersionInfo{0, 0, 3, 0})
}

// GetVersion returns the current version of the Oasis user app
func (ledger *LedgerOasis) GetVersion() (*VersionInfo, error) {
	message := []byte{CLA, INSGetVersion, 0, 0, 0}
	response, err := ledger.api.Exchange(message)

	if err != nil {
		return nil, err
	}

	if len(response) < 4 {
		return nil, fmt.Errorf("invalid response")
	}

	ledger.version = VersionInfo{
		AppMode: response[0],
		Major:   response[1],
		Minor:   response[2],
		Patch:   response[3],
	}

	return &ledger.version, nil
}

// SignEd25519 signs a transaction using Oasis user app
// this command requires user confirmation in the device
func (ledger *LedgerOasis) SignEd25519(bip32Path []uint32, transaction []byte) ([]byte, error) {
	return ledger.sign(bip32Path, transaction)
}

// GetPublicKeyEd25519 retrieves the public key for the corresponding bip32 derivation path (compressed)
// this command DOES NOT require user confirmation in the device
func (ledger *LedgerOasis) GetPublicKeyEd25519(bip32Path []uint32) ([]byte, error) {
	pubkey, _, err := ledger.getAddressPubKeyEd25519(bip32Path, false)
	return pubkey, err
}

// GetAddressPubKeyEd25519 returns the pubkey (compressed) and address (bech(
// this command requires user confirmation in the device
func (ledger *LedgerOasis) GetAddressPubKeyEd25519(bip32Path []uint32) (pubkey []byte, addr string, err error) {
	return ledger.getAddressPubKeyEd25519(bip32Path, true)
}

func (ledger *LedgerOasis) GetBip44bytes(bip32Path []uint32, hardenCount int) ([]byte, error) {
	pathBytes, err := GetBip44bytes(bip32Path, hardenCount)
	if err != nil {
		return nil, err
	}

	return pathBytes, nil
}

func (ledger *LedgerOasis) sign(bip32Path []uint32, transaction []byte) ([]byte, error) {
	var packetIndex byte = 1
	var packetCount = 1 + byte(math.Ceil(float64(len(transaction))/float64(userMessageChunkSize)))

	var finalResponse []byte

	var message []byte

	for packetIndex <= packetCount {
		chunk := userMessageChunkSize
		if packetIndex == 1 {
			pathBytes, err := ledger.GetBip44bytes(bip32Path, 5)
			if err != nil {
				return nil, err
			}
			header := []byte{CLA, INSSignEd25519, PayloadChunkInit, 0, byte(len(pathBytes))}
			message = append(header, pathBytes...)
		} else {
			if len(transaction) < userMessageChunkSize {
				chunk = len(transaction)
			}

			payloadDesc := byte(PayloadChunkAdd)
			if packetIndex == packetCount {
				payloadDesc = byte(PayloadChunkLast)
			}

			header := []byte{CLA, INSSignEd25519, payloadDesc, 0, byte(chunk)}
			message = append(header, transaction[:chunk]...)
		}

		response, err := ledger.api.Exchange(message)
		if err != nil {
			// FIXME: CBOR will be used instead
			if err.Error() == "[APDU_CODE_BAD_KEY_HANDLE] The parameters in the data field are incorrect" {
				// In this special case, we can extract additional info
				errorMsg := string(response)
				return nil, fmt.Errorf(errorMsg)
			}
			if err.Error() == "[APDU_CODE_DATA_INVALID] Referenced data reversibly blocked (invalidated)" {
				errorMsg := string(response)
				return nil, fmt.Errorf(errorMsg)
			}
			return nil, err
		}

		finalResponse = response
		if packetIndex > 1 {
			transaction = transaction[chunk:]
		}
		packetIndex++

	}
	return finalResponse, nil
}

// GetAddressPubKeyEd25519 returns the pubkey (compressed) and address (bech(
// this command requires user confirmation in the device
func (ledger *LedgerOasis) getAddressPubKeyEd25519(bip32Path []uint32, requireConfirmation bool) (pubkey []byte, addr string, err error) {
	pathBytes, err := ledger.GetBip44bytes(bip32Path, 5)
	if err != nil {
		return nil, "", err
	}

	p1 := byte(0)
	if requireConfirmation {
		p1 = byte(1)
	}

	// Prepare message
	header := []byte{CLA, INSGetAddrEd25519, p1, 0, 0}
	message := append(header, pathBytes...)
	message[4] = byte(len(message) - len(header)) // update length

	response, err := ledger.api.Exchange(message)

	if err != nil {
		return nil, "", err
	}
	if len(response) < 39 {
		return nil, "", fmt.Errorf("Invalid response")
	}

	pubkey = response[0:32]
	addr = string(response[32:])

	return pubkey, addr, err
}
