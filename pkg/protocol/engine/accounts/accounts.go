package accounts

import (
	"io"
	"strconv"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	"github.com/iotaledger/hive.go/stringify"
	"github.com/iotaledger/iota-core/pkg/model"
	iotago "github.com/iotaledger/iota.go/v4"
)

//nolint:revive
type AccountsData []*AccountData

type AccountData struct {
	mutex syncutils.RWMutex

	id              iotago.AccountID
	credits         *BlockIssuanceCredits
	expirySlot      iotago.SlotIndex
	outputID        iotago.OutputID
	blockIssuerKeys iotago.BlockIssuerKeys

	validatorStake                        iotago.BaseToken
	delegationStake                       iotago.BaseToken
	fixedCost                             iotago.Mana
	stakeEndEpoch                         iotago.EpochIndex
	latestSupportedProtocolVersionAndHash model.VersionAndHash
}

// Getters.
func (a *AccountData) ID() iotago.AccountID {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.id
}

func (a *AccountData) Credits() *BlockIssuanceCredits {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.credits
}

func (a *AccountData) ExpirySlot() iotago.SlotIndex {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.expirySlot
}

func (a *AccountData) OutputID() iotago.OutputID {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.outputID
}

func (a *AccountData) BlockIssuerKeys() iotago.BlockIssuerKeys {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.blockIssuerKeys
}

func (a *AccountData) ValidatorStake() iotago.BaseToken {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.validatorStake
}

func (a *AccountData) DelegationStake() iotago.BaseToken {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.delegationStake
}

func (a *AccountData) FixedCost() iotago.Mana {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.fixedCost
}

func (a *AccountData) StakeEndEpoch() iotago.EpochIndex {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.stakeEndEpoch
}
func (a *AccountData) LatestSupportedProtocolVersionAndHash() model.VersionAndHash {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.latestSupportedProtocolVersionAndHash
}

// Setters.
func (a *AccountData) SetID(id iotago.AccountID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.id = id
}

func (a *AccountData) SetCredits(credits *BlockIssuanceCredits) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.credits = credits
}

func (a *AccountData) SetExpirySlot(expirySlot iotago.SlotIndex) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.expirySlot = expirySlot
}

func (a *AccountData) SetOutputID(outputID iotago.OutputID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.outputID = outputID
}

func (a *AccountData) SetBlockIssuerKeys(blockIssuerKeys iotago.BlockIssuerKeys) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.blockIssuerKeys = blockIssuerKeys
}

func (a *AccountData) SetValidatorStake(validatorStake iotago.BaseToken) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.validatorStake = validatorStake
}

func (a *AccountData) SetDelegationStake(delegationStake iotago.BaseToken) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.delegationStake = delegationStake
}

func (a *AccountData) SetFixedCost(fixedCost iotago.Mana) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.fixedCost = fixedCost
}

func (a *AccountData) SetStakeEndEpoch(stakeEndEpoch iotago.EpochIndex) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.stakeEndEpoch = stakeEndEpoch
}

func (a *AccountData) SetLatestSupportedProtocolVersionAndHash(latestSupportedProtocolVersionAndHash model.VersionAndHash) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.latestSupportedProtocolVersionAndHash = latestSupportedProtocolVersionAndHash
}

func NewAccountData(id iotago.AccountID, opts ...options.Option[AccountData]) *AccountData {
	return options.Apply(&AccountData{
		id:                                    id,
		credits:                               &BlockIssuanceCredits{},
		expirySlot:                            0,
		outputID:                              iotago.EmptyOutputID,
		blockIssuerKeys:                       iotago.NewBlockIssuerKeys(),
		validatorStake:                        0,
		delegationStake:                       0,
		fixedCost:                             0,
		stakeEndEpoch:                         0,
		latestSupportedProtocolVersionAndHash: model.VersionAndHash{},
	}, opts)
}

func (a *AccountData) AddBlockIssuerKeys(blockIssuerKeys ...iotago.BlockIssuerKey) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	for _, blockIssuerKey := range blockIssuerKeys {
		a.blockIssuerKeys.Add(blockIssuerKey)
	}
}

func (a *AccountData) RemoveBlockIssuerKeys(blockIssuerKeys ...iotago.BlockIssuerKey) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	for _, blockIssuerKey := range blockIssuerKeys {
		a.blockIssuerKeys.Remove(blockIssuerKey)
	}
}

func (a *AccountData) Clone() *AccountData {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return &AccountData{
		id:              a.id,
		credits:         NewBlockIssuanceCredits(a.credits.Value(), a.credits.UpdateSlot()),
		expirySlot:      a.expirySlot,
		outputID:        a.outputID,
		blockIssuerKeys: a.blockIssuerKeys.Clone(),

		validatorStake:                        a.validatorStake,
		delegationStake:                       a.delegationStake,
		fixedCost:                             a.fixedCost,
		stakeEndEpoch:                         a.stakeEndEpoch,
		latestSupportedProtocolVersionAndHash: a.latestSupportedProtocolVersionAndHash,
	}
}

func AccountDataFromReader(reader io.ReadSeeker) (*AccountData, error) {
	accountID, err := stream.Read[iotago.AccountID](reader)
	if err != nil {
		return nil, ierrors.Wrap(err, "unable to read accountID")
	}

	a := NewAccountData(accountID)
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.credits, err = stream.ReadObject(reader, BlockIssuanceCreditsBytesLength, BlockIssuanceCreditsFromBytes); err != nil {
		return nil, ierrors.Wrap(err, "unable to read credits")
	}
	if a.expirySlot, err = stream.Read[iotago.SlotIndex](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read expiry slot")
	}
	if a.outputID, err = stream.Read[iotago.OutputID](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read outputID")
	}

	if a.blockIssuerKeys, err = stream.ReadObjectFromReader(reader, iotago.BlockIssuerKeysFromReader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read block issuer keys")
	}

	if a.validatorStake, err = stream.Read[iotago.BaseToken](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read validator stake")
	}

	if a.delegationStake, err = stream.Read[iotago.BaseToken](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read delegation stake")
	}

	if a.fixedCost, err = stream.Read[iotago.Mana](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read fixed cost")
	}

	if a.stakeEndEpoch, err = stream.Read[iotago.EpochIndex](reader); err != nil {
		return nil, ierrors.Wrap(err, "unable to read stake end epoch")
	}

	if a.latestSupportedProtocolVersionAndHash, err = stream.ReadObject(reader, model.VersionAndHashSize, model.VersionAndHashFromBytes); err != nil {
		return nil, ierrors.Wrap(err, "unable to read latest supported protocol version and hash")
	}

	return a, nil
}

func AccountDataFromBytes(b []byte) (*AccountData, int, error) {
	reader := stream.NewByteReader(b)

	a, err := AccountDataFromReader(reader)

	return a, reader.BytesRead(), err
}

func (a *AccountData) Bytes() ([]byte, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	byteBuffer := stream.NewByteBuffer()

	if err := stream.Write(byteBuffer, a.id); err != nil {
		return nil, ierrors.Wrap(err, "failed to write AccountID")
	}
	if err := stream.WriteObject(byteBuffer, a.credits, (*BlockIssuanceCredits).Bytes); err != nil {
		return nil, ierrors.Wrap(err, "failed to write Credits")
	}
	if err := stream.Write(byteBuffer, a.expirySlot); err != nil {
		return nil, ierrors.Wrap(err, "failed to write ExpirySlot")
	}
	if err := stream.Write(byteBuffer, a.outputID); err != nil {
		return nil, ierrors.Wrap(err, "failed to write OutputID")
	}

	if err := stream.WriteObject(byteBuffer, a.blockIssuerKeys, iotago.BlockIssuerKeys.Bytes); err != nil {
		return nil, ierrors.Wrap(err, "failed to write BlockIssuerKeys")
	}

	if err := stream.Write(byteBuffer, a.validatorStake); err != nil {
		return nil, ierrors.Wrap(err, "failed to write ValidatorStake")
	}
	if err := stream.Write(byteBuffer, a.delegationStake); err != nil {
		return nil, ierrors.Wrap(err, "failed to write DelegationStake")
	}
	if err := stream.Write(byteBuffer, a.fixedCost); err != nil {
		return nil, ierrors.Wrap(err, "failed to write FixedCost")
	}
	if err := stream.Write(byteBuffer, a.stakeEndEpoch); err != nil {
		return nil, ierrors.Wrap(err, "failed to write StakeEndEpoch")
	}
	if err := stream.WriteObject(byteBuffer, a.latestSupportedProtocolVersionAndHash, model.VersionAndHash.Bytes); err != nil {
		return nil, ierrors.Wrap(err, "failed to write LatestSupportedProtocolVersionAndHash")
	}

	return byteBuffer.Bytes()
}

func (a *AccountData) String() string {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return stringify.Struct("AccountData",
		stringify.NewStructField("ID", a.id),
		stringify.NewStructField("Credits", a.credits),
		stringify.NewStructField("ExpirySlot", uint32(a.expirySlot)),
		stringify.NewStructField("OutputID", a.outputID),
		stringify.NewStructField("BlockIssuerKeys", func() string { return strconv.Itoa(len(a.blockIssuerKeys)) }()),
		stringify.NewStructField("ValidatorStake", uint64(a.validatorStake)),
		stringify.NewStructField("DelegationStake", uint64(a.delegationStake)),
		stringify.NewStructField("FixedCost", uint64(a.fixedCost)),
		stringify.NewStructField("StakeEndEpoch", uint64(a.stakeEndEpoch)),
		stringify.NewStructField("LatestSupportedProtocolVersionAndHash", a.latestSupportedProtocolVersionAndHash),
	)
}

func WithCredits(credits *BlockIssuanceCredits) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetCredits(credits)
	}
}

func WithExpirySlot(expirySlot iotago.SlotIndex) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetExpirySlot(expirySlot)
	}
}

func WithOutputID(outputID iotago.OutputID) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetOutputID(outputID)
	}
}

func WithBlockIssuerKeys(blockIssuerKeys ...iotago.BlockIssuerKey) options.Option[AccountData] {
	return func(a *AccountData) {
		a.AddBlockIssuerKeys(blockIssuerKeys...)
	}
}

func WithValidatorStake(validatorStake iotago.BaseToken) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetValidatorStake(validatorStake)
	}
}

func WithDelegationStake(delegationStake iotago.BaseToken) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetDelegationStake(delegationStake)
	}
}

func WithFixedCost(fixedCost iotago.Mana) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetFixedCost(fixedCost)
	}
}

func WithStakeEndEpoch(stakeEndEpoch iotago.EpochIndex) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetStakeEndEpoch(stakeEndEpoch)
	}
}

func WithLatestSupportedProtocolVersionAndHash(versionAndHash model.VersionAndHash) options.Option[AccountData] {
	return func(a *AccountData) {
		a.SetLatestSupportedProtocolVersionAndHash(versionAndHash)
	}
}
