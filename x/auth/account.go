package auth

import (
	"errors"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/tendermint/tendermint/crypto"
)

// Account is an interface used to store coins at a given address within state.
// It presumes a notion of sequence numbers for replay protection,
// a notion of account numbers for replay protection for previously pruned accounts,
// and a pubkey for authentication purposes.
//
// Many complex conditions can be used in the concrete struct which implements Account.
type Account interface {
	GetAddress() sdk.AccAddress
	SetAddress(sdk.AccAddress) error // errors if already set.

	GetPubKey() crypto.PubKey // can return nil.
	SetPubKey(crypto.PubKey) error

	GetAccountNumber() int64
	SetAccountNumber(int64) error

	GetSequence() int64
	SetSequence(int64) error

	GetCoins() sdk.Coins
	SetCoins(sdk.Coins) error
}

// VestingAccount defines an account type that vests coins via a vesting schedule.
type VestingAccount interface {
	Account

	// Calculates the amount of coins that can be sent to other accounts given
	// the current time.
	SpendableCoins(blockTime time.Time) sdk.Coins

	TrackDelegation(blockTime time.Time, amount sdk.Coins) // Performs delegation accounting.
	TrackUndelegation(amount sdk.Coins)                    // Performs undelegation accounting.
}

// AccountDecoder unmarshals account bytes
type AccountDecoder func(accountBytes []byte) (Account, error)

//-----------------------------------------------------------------------------
// BaseAccount

var _ Account = (*BaseAccount)(nil)

// BaseAccount - a base account structure.
// This can be extended by embedding within in your AppAccount.
// There are examples of this in: examples/basecoin/types/account.go.
// However one doesn't have to use BaseAccount as long as your struct
// implements Account.
type BaseAccount struct {
	Address       sdk.AccAddress `json:"address"`
	Coins         sdk.Coins      `json:"coins"`
	PubKey        crypto.PubKey  `json:"public_key"`
	AccountNumber int64          `json:"account_number"`
	Sequence      int64          `json:"sequence"`
}

// Prototype function for BaseAccount
func ProtoBaseAccount() Account {
	return &BaseAccount{}
}

func NewBaseAccountWithAddress(addr sdk.AccAddress) BaseAccount {
	return BaseAccount{
		Address: addr,
	}
}

// Implements sdk.Account.
func (acc BaseAccount) GetAddress() sdk.AccAddress {
	return acc.Address
}

// Implements sdk.Account.
func (acc *BaseAccount) SetAddress(addr sdk.AccAddress) error {
	if len(acc.Address) != 0 {
		return errors.New("cannot override BaseAccount address")
	}
	acc.Address = addr
	return nil
}

// Implements sdk.Account.
func (acc BaseAccount) GetPubKey() crypto.PubKey {
	return acc.PubKey
}

// Implements sdk.Account.
func (acc *BaseAccount) SetPubKey(pubKey crypto.PubKey) error {
	acc.PubKey = pubKey
	return nil
}

// Implements sdk.Account.
func (acc *BaseAccount) GetCoins() sdk.Coins {
	return acc.Coins
}

// Implements sdk.Account.
func (acc *BaseAccount) SetCoins(coins sdk.Coins) error {
	acc.Coins = coins
	return nil
}

// Implements Account
func (acc *BaseAccount) GetAccountNumber() int64 {
	return acc.AccountNumber
}

// Implements Account
func (acc *BaseAccount) SetAccountNumber(accNumber int64) error {
	acc.AccountNumber = accNumber
	return nil
}

// Implements sdk.Account.
func (acc *BaseAccount) GetSequence() int64 {
	return acc.Sequence
}

// Implements sdk.Account.
func (acc *BaseAccount) SetSequence(seq int64) error {
	acc.Sequence = seq
	return nil
}

//-----------------------------------------------------------------------------
// Vesting Accounts

var (
	_ VestingAccount = (*ContinuousVestingAccount)(nil)
	// TODO: uncomment once implemented
	// _ VestingAccount = (*DelayedVestingAccount)(nil)
)

type (
	// BaseVestingAccount implements the VestingAccount interface. It contains all
	// the necessary fields needed for any vesting account implementation.
	BaseVestingAccount struct {
		BaseAccount

		originalVesting sdk.Coins // coins in account upon initialization
		delegatedFree   sdk.Coins // coins that are vested and delegated
		endTime         time.Time // when the coins become unlocked
	}

	// ContinuousVestingAccount implements the VestingAccount interface. It
	// continuously vests by unlocking coins linearly with respect to time.
	ContinuousVestingAccount struct {
		BaseVestingAccount

		delegatedVesting sdk.Coins // coins that vesting and delegated
		startTime        time.Time // when the coins start to vest
	}

	// DelayedVestingAccount implements the VestingAccount interface. It vests all
	// coins after a specific time, but non prior. In other words, it keeps them
	// locked until a specified time.
	DelayedVestingAccount struct {
		BaseVestingAccount
	}
)

func NewContinuousVestingAccount(
	addr sdk.AccAddress, origCoins sdk.Coins, startTime, endTime time.Time,
) *ContinuousVestingAccount {

	baseAcc := BaseAccount{
		Address: addr,
		Coins:   origCoins,
	}

	baseVestingAcc := BaseVestingAccount{
		BaseAccount:     baseAcc,
		originalVesting: origCoins,
		endTime:         endTime,
	}

	return &ContinuousVestingAccount{
		startTime:          startTime,
		BaseVestingAccount: baseVestingAcc,
	}
}

// GetVestedCoins returns the total number of vested coins. If no coins are vested,
// nil is returned.
func (cva ContinuousVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	var vestedCoins sdk.Coins

	// We must handle the case where the start time for a vesting account has
	// been set into the future or when the start of the chain is not exactly
	// known.
	if blockTime.Unix() <= cva.startTime.Unix() {
		return vestedCoins
	}

	// calculate the vesting scalar
	x := blockTime.Unix() - cva.startTime.Unix()
	y := cva.endTime.Unix() - cva.startTime.Unix()
	s := sdk.NewDec(x).Quo(sdk.NewDec(y))

	for _, ovc := range cva.originalVesting {
		vestedAmt := sdk.NewDecFromInt(ovc.Amount).Mul(s).RoundInt()
		vestedCoin := sdk.NewCoin(ovc.Denom, vestedAmt)
		vestedCoins = vestedCoins.Plus(sdk.Coins{vestedCoin})
	}

	return vestedCoins
}

// GetVestingCoins returns the total number of vesting coins. If no coins are
// vesting, nil is returned.
func (cva ContinuousVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return cva.originalVesting.Minus(cva.GetVestedCoins(blockTime))
}

// SpendableCoins returns the total number of spendable coins per denom for a
// continuous vesting account.
func (cva ContinuousVestingAccount) SpendableCoins(blockTime time.Time) sdk.Coins {
	var spendableCoins sdk.Coins

	bc := cva.GetCoins()
	v := cva.GetVestingCoins(blockTime)

	for _, coin := range bc {
		baseAmt := coin.Amount
		delVestingAmt := cva.delegatedVesting.AmountOf(coin.Denom)
		vestingAmt := v.AmountOf(coin.Denom)

		// compute min((BC + DV) - V, BC) per the specification
		min := sdk.MinInt(baseAmt.Add(delVestingAmt).Sub(vestingAmt), baseAmt)
		spendableCoin := sdk.NewCoin(coin.Denom, min)

		if !spendableCoin.IsZero() {
			spendableCoins = spendableCoins.Plus(sdk.Coins{spendableCoin})
		}
	}

	return spendableCoins
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (cva *ContinuousVestingAccount) TrackDelegation(blockTime time.Time, amount sdk.Coins) {
	bc := cva.GetCoins()
	v := cva.GetVestingCoins(blockTime)

	for _, coin := range amount {
		// Skip if the delegation amount is zero or if the base coins does not
		// exceed the desired delegation amount.
		if coin.Amount.IsZero() || bc.AmountOf(coin.Denom).LT(coin.Amount) {
			continue
		}

		vestingAmt := v.AmountOf(coin.Denom)
		delVestingAmt := cva.delegatedVesting.AmountOf(coin.Denom)

		// compute x and y per the specification, where:
		// X := min(max(V - DV, 0), D)
		// Y := D - X
		x := sdk.MinInt(sdk.MaxInt(vestingAmt.Sub(delVestingAmt), sdk.ZeroInt()), coin.Amount)
		y := coin.Amount.Sub(x)

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			cva.delegatedVesting = cva.delegatedVesting.Plus(sdk.Coins{xCoin})
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			cva.delegatedFree = cva.delegatedFree.Plus(sdk.Coins{yCoin})
		}

		cva.Coins = bc.Minus(sdk.Coins{coin})
	}
}

// TrackUndelegation tracks an undelegation amount by setting the necessary
// values by which delegated vesting and delegated vesting need to decrease and
// by which amount the base coins need to increase.
func (cva *ContinuousVestingAccount) TrackUndelegation(amount sdk.Coins) {
	bc := cva.GetCoins()

	for _, coin := range amount {
		// skip if the undelegation amount is zero
		if coin.Amount.IsZero() {
			continue
		}

		delegatedFree := cva.delegatedFree.AmountOf(coin.Denom)

		// compute x and y per the specification, where:
		// X := min(DF, D)
		// Y := D - X
		x := sdk.MinInt(delegatedFree, coin.Amount)
		y := coin.Amount.Sub(x)

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			cva.delegatedFree = cva.delegatedFree.Minus(sdk.Coins{xCoin})
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			cva.delegatedVesting = cva.delegatedVesting.Minus(sdk.Coins{yCoin})
		}

		cva.Coins = bc.Plus(sdk.Coins{coin})
	}
}

//-----------------------------------------------------------------------------
// Codec

// Most users shouldn't use this, but this comes in handy for tests.
func RegisterBaseAccount(cdc *codec.Codec) {
	cdc.RegisterInterface((*Account)(nil), nil)
	cdc.RegisterInterface((*VestingAccount)(nil), nil)
	cdc.RegisterConcrete(&BaseAccount{}, "cosmos-sdk/BaseAccount", nil)
	cdc.RegisterConcrete(&ContinuousVestingAccount{}, "cosmos-sdk/ContinuousVestingAccount", nil)
	cdc.RegisterConcrete(&DelayedVestingAccount{}, "cosmos-sdk/DelayedVestingAccount", nil)
	codec.RegisterCrypto(cdc)
}
