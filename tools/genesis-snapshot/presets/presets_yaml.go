package presets

import (
	"fmt"
	"time"

	"golang.org/x/crypto/blake2b"

	"github.com/iotaledger/hive.go/crypto/ed25519"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/ioutils"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/pkg/testsuite/snapshotcreator"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/hexutil"
)

type ValidatorYaml struct {
	Name                 string `yaml:"name"`
	PublicKey            string `yaml:"publicKey"`
	Amount               uint64 `yaml:"amount"`
	Mana                 uint64 `yaml:"mana"`
	BlockIssuanceCredits uint64 `yaml:"blockIssuanceCredits"`
	FixedCost            uint64 `yaml:"fixedCost"`
}

type BlockIssuerYaml struct {
	Name                 string `yaml:"name"`
	PublicKey            string `yaml:"publicKey"`
	Amount               uint64 `yaml:"amount"`
	Mana                 uint64 `yaml:"mana"`
	BlockIssuanceCredits uint64 `yaml:"blockIssuanceCredits"`
}

type BasicOutputYaml struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
	Amount  uint64 `yaml:"amount"`
	Mana    uint64 `yaml:"mana"`
}

type ConfigYaml struct {
	NetworkName           string `yaml:"networkName"`
	Bech32HRP             string `yaml:"bech32Hrp"`
	TargetCommitteeSize   uint8  `yaml:"targetCommitteeSize"`
	SlotsPerEpochExponent uint8  `yaml:"slotsPerEpochExponent"`

	FilePath string `yaml:"filepath"`

	Validators   []ValidatorYaml   `yaml:"validators"`
	BlockIssuers []BlockIssuerYaml `yaml:"blockIssuers"`
	BasicOutputs []BasicOutputYaml `yaml:"basicOutputs"`
}

func TestnetProtocolParameters(networkName string, bech32HRP iotago.NetworkPrefix, targetCommitteeSize uint8, slotsPerEpochExponent uint8) iotago.ProtocolParameters {
	return iotago.NewV3SnapshotProtocolParameters(
		iotago.WithNetworkOptions(networkName, bech32HRP),
		iotago.WithStorageOptions(100, 1, 100, 1000, 1000, 1000),
		iotago.WithWorkScoreOptions(500, 110_000, 7_500, 40_000, 90_000, 50_000, 40_000, 70_000, 5_000, 15_000),
		iotago.WithTimeProviderOptions(0, time.Now().Unix(), 10, slotsPerEpochExponent),
		iotago.WithLivenessOptions(10, 15, 4, 7, 100),
		iotago.WithSupplyOptions(4600000000000000, 63, 1, 17, 32, 21, 70),
		iotago.WithCongestionControlOptions(1, 1, 1, 400_000_000, 250_000_000, 50_000_000, 1000, 100),
		iotago.WithStakingOptions(3, 10, 10),
		iotago.WithVersionSignalingOptions(7, 5, 7),
		iotago.WithRewardsOptions(8, 11, 2, 384),
		iotago.WithTargetCommitteeSize(targetCommitteeSize),
		iotago.WithChainSwitchingThreshold(10),
	)
}

func GenerateFromYaml(hostsFile string) ([]options.Option[snapshotcreator.Options], error) {
	var configYaml ConfigYaml
	if err := ioutils.ReadYAMLFromFile(hostsFile, &configYaml); err != nil {
		return nil, err
	}

	if configYaml.TargetCommitteeSize == 0 {
		return nil, ierrors.Errorf("targetCommitteeSize must be greater than 0")
	}

	if configYaml.SlotsPerEpochExponent == 0 {
		return nil, ierrors.Errorf("slotsPerEpochExponent must be greater than 0")
	}

	fmt.Printf("generating protocol parameters for network: %s, bech32HRP: %s, targetCommitteeSize: %d, slotsPerEpochExponent: %d\n", configYaml.NetworkName, configYaml.Bech32HRP, configYaml.TargetCommitteeSize, configYaml.SlotsPerEpochExponent)
	protocolParams := TestnetProtocolParameters(configYaml.NetworkName, iotago.NetworkPrefix(configYaml.Bech32HRP), configYaml.TargetCommitteeSize, configYaml.SlotsPerEpochExponent)

	accounts := make([]snapshotcreator.AccountDetails, 0, len(configYaml.Validators)+len(configYaml.BlockIssuers))
	for _, validator := range configYaml.Validators {
		pubKey := validator.PublicKey
		stake := lo.Max(mock.MinValidatorAccountAmount(protocolParams), iotago.BaseToken(validator.Amount))
		mana := lo.Max(iotago.Mana(mock.MinValidatorAccountAmount(protocolParams)), iotago.Mana(validator.Mana))
		fixedCost := lo.Max(1, iotago.Mana(validator.FixedCost))
		fmt.Printf("adding validator %s with publicKey %s, stake %d, mana %d and fixedCost %d\n", validator.Name, pubKey, stake, mana, fixedCost)
		account := snapshotcreator.AccountDetails{
			AccountID:            blake2b.Sum256(lo.PanicOnErr(hexutil.DecodeHex(pubKey))),
			Address:              iotago.Ed25519AddressFromPubKey(lo.PanicOnErr(hexutil.DecodeHex(pubKey))),
			Amount:               stake,
			IssuerKey:            iotago.Ed25519PublicKeyHashBlockIssuerKeyFromPublicKey(ed25519.PublicKey(lo.PanicOnErr(hexutil.DecodeHex(pubKey)))),
			ExpirySlot:           iotago.MaxSlotIndex,
			BlockIssuanceCredits: iotago.BlockIssuanceCredits(validator.BlockIssuanceCredits),
			StakingEndEpoch:      iotago.MaxEpochIndex,
			FixedCost:            fixedCost,
			StakedAmount:         stake,
			Mana:                 mana,
		}
		accounts = append(accounts, account)
	}

	for _, blockIssuer := range configYaml.BlockIssuers {
		pubKey := blockIssuer.PublicKey
		amount := lo.Max(mock.MinValidatorAccountAmount(protocolParams), iotago.BaseToken(blockIssuer.Amount))
		mana := lo.Max(iotago.Mana(mock.MinValidatorAccountAmount(protocolParams)), iotago.Mana(blockIssuer.Mana))
		fmt.Printf("adding blockissuer %s with publicKey %s, amount %d, mana %d\n", blockIssuer.Name, pubKey, amount, mana)
		account := snapshotcreator.AccountDetails{
			AccountID:            blake2b.Sum256(lo.PanicOnErr(hexutil.DecodeHex(pubKey))),
			Address:              iotago.Ed25519AddressFromPubKey(lo.PanicOnErr(hexutil.DecodeHex(pubKey))),
			Amount:               amount,
			IssuerKey:            iotago.Ed25519PublicKeyHashBlockIssuerKeyFromPublicKey(ed25519.PublicKey(lo.PanicOnErr(hexutil.DecodeHex(pubKey)))),
			ExpirySlot:           iotago.MaxSlotIndex,
			BlockIssuanceCredits: iotago.BlockIssuanceCredits(blockIssuer.BlockIssuanceCredits),
			Mana:                 mana,
		}
		accounts = append(accounts, account)
	}

	basicOutputs := make([]snapshotcreator.BasicOutputDetails, 0, len(configYaml.BasicOutputs))
	for _, basicOutput := range configYaml.BasicOutputs {
		hrp, address, err := iotago.ParseBech32(basicOutput.Address)
		if err != nil {
			panic(err)
		}
		if protocolParams.Bech32HRP() != hrp {
			panic(fmt.Sprintf("address %s has wrong HRP %s, expected %s", address, hrp, protocolParams.Bech32HRP()))
		}
		amount := basicOutput.Amount
		mana := basicOutput.Mana
		fmt.Printf("adding basicOutput %s for %s with amount %d and mana %d\n", basicOutput.Name, address, amount, mana)
		basicOutputs = append(basicOutputs, snapshotcreator.BasicOutputDetails{
			Address: address,
			Amount:  iotago.BaseToken(amount),
			Mana:    iotago.Mana(mana),
		})
	}

	return []options.Option[snapshotcreator.Options]{
		snapshotcreator.WithFilePath(configYaml.FilePath),
		snapshotcreator.WithProtocolParameters(protocolParams),
		snapshotcreator.WithAccounts(accounts...),
		snapshotcreator.WithBasicOutputs(basicOutputs...),
	}, nil
}
