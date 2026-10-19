package polymarket

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// The wallets a key controls, as @polymarket/client derives them: the key's own address,
// or the proxy wallet, Safe or Deposit Wallet Polymarket deploys for it at a CREATE2
// address. Checked against vectors the client generates (D151).

// Derivation is what Polymarket derives an account's wallets from.
type Derivation struct {
	ProxyFactory, ProxyImplementation common.Address
	SafeFactory                       common.Address
	SafeInitCodeHash                  common.Hash
	DepositWalletFactory              common.Address
	DepositWalletBeacon               common.Address
	DepositWalletImplementation       common.Address
}

// Production is Polygon's.
var Production = Derivation{
	ProxyFactory:                common.HexToAddress("0xaB45c5A4B0c941a2F231C04C3f49182e1A254052"),
	ProxyImplementation:         common.HexToAddress("0x44e999d5c2F66Ef0861317f9A4805AC2e90aEB4f"),
	SafeFactory:                 common.HexToAddress("0xaacFeEa03eb1561C4e67d661e40682Bd20E3541b"),
	SafeInitCodeHash:            common.HexToHash("0x2bce2127ff07fb632d16c8347c4ebf501f4841168bed00d9e6ef715ddb6fcecf"),
	DepositWalletFactory:        common.HexToAddress("0x00000000000Fb5C9ADea0298D729A0CB3823Cc07"),
	DepositWalletBeacon:         common.HexToAddress("0x7A18EDfe055488A3128f01F563e5B479D92ffc3a"),
	DepositWalletImplementation: common.HexToAddress("0x58CA52ebe0DadfdF531Cde7062e76746de4Db1eB"),
}

// Wallets are the wallets a signer's key controls.
type Wallets struct {
	Proxy, Safe                common.Address
	DepositBeacon, DepositUUPS common.Address // a Deposit Wallet is one of the two
}

// WalletsOf derives the wallets signer controls.
func (d Derivation) WalletsOf(signer common.Address) Wallets {
	return Wallets{
		Proxy:         d.proxy(signer),
		Safe:          d.safe(signer),
		DepositBeacon: d.depositBeacon(signer),
		DepositUUPS:   d.depositUUPS(signer),
	}
}

// Classify says how signer's key controls wallet, as the official client classifies an
// account: as the wallet itself, a Deposit Wallet, a Safe or a proxy wallet; false when it
// controls none of them.
func (d Derivation) Classify(signer, wallet common.Address) (SignatureType, bool) {
	if signer == wallet {
		return SignatureEOA, true
	}

	w := d.WalletsOf(signer)

	switch wallet {
	case w.DepositBeacon, w.DepositUUPS:
		return SignatureDepositWallet, true
	case w.Safe:
		return SignatureSafe, true
	case w.Proxy:
		return SignatureProxy, true
	}

	return 0, false
}

const proxyBytecode = "3d3d606380380380913d393d73%s5af4602a57600080fd5b602d8060366000396000f3363d3d373d3d3d363d73" +
	"%s5af43d82803e903d91602b57fd5bf352e831dd00000000000000000000000000000000000000000000000000000000000000" +
	"200000000000000000000000000000000000000000000000000000000000000000"

func (d Derivation) proxy(signer common.Address) common.Address {
	code := strings.Replace(proxyBytecode, "%s", hexLower(d.ProxyFactory), 1)
	code = strings.Replace(code, "%s", hexLower(d.ProxyImplementation), 1)

	return crypto.CreateAddress2(d.ProxyFactory, crypto.Keccak256Hash(signer.Bytes()), crypto.Keccak256(common.FromHex(code)))
}

func (d Derivation) safe(signer common.Address) common.Address {
	return crypto.CreateAddress2(d.SafeFactory, crypto.Keccak256Hash(common.LeftPadBytes(signer.Bytes(), 32)), d.SafeInitCodeHash.Bytes())
}

var (
	erc1967Const1    = common.FromHex("0xcc3735a920a3ca505d382bbc545af43d6000803e6038573d6000fd5b3d6000f3")
	erc1967Const2    = common.FromHex("0x5155f3363d3d373d3d363d7f360894a13ba1a3210667c828492db98dca3e2076")
	erc1967Prefix, _ = new(big.Int).SetString("61003d3d8160233d3973", 16)

	beaconConst1    = common.FromHex("0xb3582b35133d50545afa5036515af43d6000803e604d573d6000fd5b3d6000f3")
	beaconConst2    = common.FromHex("0x1b60e01b36527fa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6c")
	beaconConst3    = common.FromHex("0x60195155f3363d3d373d3d363d602036600436635c60da")
	beaconPrefix, _ = new(big.Int).SetString("6100523d8160233d3973", 16)
)

// depositArgs are a Deposit Wallet's constructor arguments: its factory, and the signer
// as its ID, ABI-encoded.
func (d Derivation) depositArgs(signer common.Address) []byte {
	return append(common.LeftPadBytes(d.DepositWalletFactory.Bytes(), 32), common.LeftPadBytes(signer.Bytes(), 32)...)
}

// prefixed is the 10-byte prefix of an ERC-1967 proxy's init code, with its arguments'
// length in its bits from 56.
func prefixed(prefix *big.Int, args []byte) []byte {
	n := new(big.Int).Lsh(big.NewInt(int64(len(args))), 56)
	return common.LeftPadBytes(new(big.Int).Add(prefix, n).Bytes(), 10)
}

func (d Derivation) depositUUPS(signer common.Address) common.Address {
	args := d.depositArgs(signer)
	init := concat(prefixed(erc1967Prefix, args), d.DepositWalletImplementation.Bytes(), common.FromHex("0x6009"),
		erc1967Const2, erc1967Const1, args)

	return crypto.CreateAddress2(d.DepositWalletFactory, crypto.Keccak256Hash(args), crypto.Keccak256(init))
}

func (d Derivation) depositBeacon(signer common.Address) common.Address {
	args := d.depositArgs(signer)
	init := concat(prefixed(beaconPrefix, args), d.DepositWalletBeacon.Bytes(), beaconConst3, beaconConst2, beaconConst1, args)

	return crypto.CreateAddress2(d.DepositWalletFactory, crypto.Keccak256Hash(args), crypto.Keccak256(init))
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}

	return out
}

func hexLower(a common.Address) string {
	return strings.ToLower(strings.TrimPrefix(a.Hex(), "0x"))
}
