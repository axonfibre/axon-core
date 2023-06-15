package testsuite

import (
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"time"

	"golang.org/x/xerrors"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/utxoledger"
	"github.com/iotaledger/iota-core/pkg/protocol/snapshotcreator"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
	"github.com/iotaledger/iota.go/v4/tpkg"
)

type TransactionFramework struct {
	api         iotago.API
	protoParams *iotago.ProtocolParameters

	wallet       *mock.HDWallet
	states       map[string]*utxoledger.Output
	transactions map[string]*iotago.Transaction
}

func NewTransactionFramework(protocol *protocol.Protocol, genesisSeed []byte, accounts ...snapshotcreator.AccountDetails) *TransactionFramework {
	// The genesis output is on index 0 of the genesis TX
	genesisOutput, err := protocol.MainEngineInstance().Ledger.Output(iotago.OutputID{}.UTXOInput())
	if err != nil {
		panic(err)
	}

	tf := &TransactionFramework{
		api:          protocol.API(),
		protoParams:  protocol.MainEngineInstance().Storage.Settings().ProtocolParameters(),
		states:       map[string]*utxoledger.Output{"Genesis:0": genesisOutput},
		transactions: make(map[string]*iotago.Transaction),
		wallet:       mock.NewHDWallet("genesis", genesisSeed, 0),
	}

	for idx := range accounts {
		// Genesis TX
		outputID := iotago.OutputID{}
		// Accounts start from index 1 of the genesis TX
		binary.LittleEndian.PutUint16(outputID[iotago.TransactionIDLength:], uint16(idx+1))
		if tf.states[fmt.Sprintf("Genesis:%d", idx+1)], err = protocol.MainEngineInstance().Ledger.Output(outputID.UTXOInput()); err != nil {
			panic(err)
		}
	}

	return tf
}

func (t *TransactionFramework) RegisterTransaction(alias string, transaction *iotago.Transaction) {
	(lo.PanicOnErr(transaction.ID())).RegisterAlias(alias)

	t.transactions[alias] = transaction

	for outputID, output := range lo.PanicOnErr(transaction.OutputsSet()) {
		clonedOutput := output.Clone()
		actualOutputID := iotago.OutputIDFromTransactionIDAndIndex(lo.PanicOnErr(transaction.ID()), outputID.Index())
		if clonedOutput.Type() == iotago.OutputAccount && clonedOutput.(*iotago.AccountOutput).AccountID == iotago.EmptyAccountID() {
			clonedOutput.(*iotago.AccountOutput).AccountID = iotago.AccountIDFromOutputID(actualOutputID)
		}

		t.states[fmt.Sprintf("%s:%d", alias, outputID.Index())] = utxoledger.CreateOutput(t.api, actualOutputID, iotago.EmptyBlockID(), 0, t.api.SlotTimeProvider().IndexFromTime(time.Now()), clonedOutput)
	}
}

func (t *TransactionFramework) CreateTransactionWithOptions(alias string, signingWallets []*mock.HDWallet, opts ...options.Option[builder.TransactionBuilder]) (*iotago.Transaction, error) {
	walletKeys := make([]iotago.AddressKeys, len(signingWallets))
	for i, wallet := range signingWallets {
		inputPrivateKey, _ := wallet.KeyPair()
		walletKeys[i] = iotago.AddressKeys{Address: wallet.Address(), Keys: inputPrivateKey}
	}

	txBuilder := builder.NewTransactionBuilder(t.protoParams.NetworkID())

	// Always add a random payload to randomize transaction ID.
	randomPayload := tpkg.Rand12ByteArray()
	txBuilder.AddTaggedDataPayload(&iotago.TaggedData{Tag: randomPayload[:], Data: randomPayload[:]})

	tx, err := options.Apply(txBuilder, opts).Build(t.protoParams, iotago.NewInMemoryAddressSigner(walletKeys...))
	if err == nil {
		t.RegisterTransaction(alias, tx)
	}

	return tx, err
}

func (t *TransactionFramework) CreateSimpleTransaction(alias string, outputCount int, inputAliases ...string) (*iotago.Transaction, error) {
	inputStates, outputStates, signingWallets := t.CreateBasicOutputsEqually(outputCount, inputAliases...)

	return t.CreateTransactionWithOptions(alias, signingWallets, WithInputs(inputStates), WithOutputs(outputStates))
}

func (t *TransactionFramework) CreateBasicOutputsEqually(outputCount int, inputAliases ...string) (consumedInputs utxoledger.Outputs, outputs iotago.Outputs[iotago.Output], signingWallets []*mock.HDWallet) {
	inputStates := make([]*utxoledger.Output, 0, len(inputAliases))
	totalInputDeposits := uint64(0)
	totalInputStoredMana := uint64(0)

	for _, inputAlias := range inputAliases {
		output := t.Output(inputAlias)
		inputStates = append(inputStates, output)
		totalInputDeposits += output.Deposit()
		totalInputStoredMana += output.StoredMana()
	}

	manaAmount := totalInputStoredMana / uint64(outputCount)
	remainderMana := totalInputStoredMana

	tokenAmount := totalInputDeposits / uint64(outputCount)
	remainderFunds := totalInputDeposits

	outputStates := make(iotago.Outputs[iotago.Output], 0, outputCount)
	for i := 0; i < outputCount; i++ {
		if i+1 == outputCount {
			tokenAmount = remainderFunds
			manaAmount = remainderMana
		}
		remainderFunds -= tokenAmount
		remainderMana -= manaAmount

		outputStates = append(outputStates, &iotago.BasicOutput{
			Amount: tokenAmount,
			Conditions: iotago.BasicOutputUnlockConditions{
				&iotago.AddressUnlockCondition{Address: t.DefaultAddress()},
			},
			Mana: manaAmount,
		})
	}

	return inputStates, outputStates, []*mock.HDWallet{t.wallet}
}

func (t *TransactionFramework) CreateBasicOutputs(depositDistribution, manaDistribution []uint64, inputAliases ...string) (consumedInputs utxoledger.Outputs, outputs iotago.Outputs[iotago.Output], signingWallets []*mock.HDWallet) {
	if len(depositDistribution) != len(manaDistribution) {
		panic("deposit and mana distributions should have the same length")
	}

	inputStates := make([]*utxoledger.Output, 0, len(inputAliases))
	totalInputDeposits := uint64(0)
	totalInputStoredMana := uint64(0)

	for _, inputAlias := range inputAliases {
		output := t.Output(inputAlias)
		inputStates = append(inputStates, output)
		totalInputDeposits += output.Deposit()
		totalInputStoredMana += output.StoredMana()
	}

	if lo.Sum(depositDistribution...) != totalInputDeposits {
		panic("deposit on input and output side must be equal")
	}

	outputStates := make(iotago.Outputs[iotago.Output], 0, len(depositDistribution))
	for idx, outputDeposit := range depositDistribution {
		outputStates = append(outputStates, &iotago.BasicOutput{
			Amount: outputDeposit,
			Conditions: iotago.BasicOutputUnlockConditions{
				&iotago.AddressUnlockCondition{Address: t.DefaultAddress()},
			},
			Mana: manaDistribution[idx],
		})
	}

	return inputStates, outputStates, []*mock.HDWallet{t.wallet}
}

func (t *TransactionFramework) CreateAccountFromInput(inputAlias string, opts ...options.Option[iotago.AccountOutput]) (utxoledger.Outputs, iotago.Outputs[iotago.Output], []*mock.HDWallet) {
	input := t.Output(inputAlias)

	accountOutput := options.Apply(&iotago.AccountOutput{
		Amount: input.Deposit(),
		Mana:   input.StoredMana(),
		Conditions: iotago.AccountOutputUnlockConditions{
			&iotago.StateControllerAddressUnlockCondition{Address: t.DefaultAddress()},
			&iotago.GovernorAddressUnlockCondition{Address: t.DefaultAddress()},
		},
	}, opts)

	outputStates := iotago.Outputs[iotago.Output]{accountOutput}

	// if amount was set by options, a remainder output needs to be created
	if accountOutput.Amount != input.Deposit() {
		outputStates = append(outputStates, &iotago.BasicOutput{
			Amount: input.Deposit() - accountOutput.Amount,
			Conditions: iotago.BasicOutputUnlockConditions{
				&iotago.AddressUnlockCondition{Address: t.DefaultAddress()},
			},
			Mana: input.StoredMana() - accountOutput.Mana,
		})
	}

	return utxoledger.Outputs{input}, outputStates, []*mock.HDWallet{t.wallet}

}

func (t *TransactionFramework) DestroyAccount(alias string) (consumedInputs *utxoledger.Output, outputs iotago.Outputs[iotago.Output], signingWallets []*mock.HDWallet) {
	output := t.Output(alias)

	outputStates := iotago.Outputs[iotago.Output]{&iotago.BasicOutput{
		Amount: output.Deposit(),
		Conditions: iotago.BasicOutputUnlockConditions{
			&iotago.AddressUnlockCondition{Address: t.DefaultAddress()},
		},
		Mana: output.StoredMana(),
	}}

	return output, outputStates, []*mock.HDWallet{t.wallet}
}

func (t *TransactionFramework) TransitionAccount(alias string, opts ...options.Option[iotago.AccountOutput]) (consumedInput *utxoledger.Output, outputs iotago.Outputs[iotago.Output], signingWallets []*mock.HDWallet) {
	output, exists := t.states[alias]
	if !exists {
		panic(fmt.Sprintf("account with alias %s does not exist", alias))
	}

	accountOutput := options.Apply(output.Output().Clone().(*iotago.AccountOutput), opts)

	return output, iotago.Outputs[iotago.Output]{accountOutput}, []*mock.HDWallet{t.wallet}
}

func (t *TransactionFramework) Output(alias string) *utxoledger.Output {
	output, exists := t.states[alias]
	if !exists {
		panic(xerrors.Errorf("output with given alias does not exist %s", alias))
	}

	return output
}

func (t *TransactionFramework) OutputID(alias string) iotago.OutputID {
	return t.Output(alias).OutputID()
}

func (t *TransactionFramework) Transaction(alias string) *iotago.Transaction {
	transaction, exists := t.transactions[alias]
	if !exists {
		panic(xerrors.Errorf("transaction with given alias does not exist %s", alias))
	}

	return transaction
}

func (t *TransactionFramework) TransactionID(alias string) iotago.TransactionID {
	return lo.PanicOnErr(t.Transaction(alias).ID())
}

func (t *TransactionFramework) Transactions(aliases ...string) []*iotago.Transaction {
	return lo.Map(aliases, t.Transaction)
}

func (t *TransactionFramework) TransactionIDs(aliases ...string) []iotago.TransactionID {
	return lo.Map(aliases, t.TransactionID)
}

func (t *TransactionFramework) DefaultAddress() iotago.Address {
	return t.wallet.Address()
}

// Account options

func WithBlockIssuerFeature(blockIssuerFeature *iotago.BlockIssuerFeature) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		for idx, feature := range accountOutput.Features {
			if feature.Type() == iotago.FeatureBlockIssuer {
				accountOutput.Features[idx] = blockIssuerFeature
				return
			}
		}

		accountOutput.Features = append(accountOutput.Features, blockIssuerFeature)
	}
}

func AddBlockIssuerKey(key ed25519.PublicKey) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		blockIssuer := accountOutput.FeatureSet().BlockIssuer()
		if blockIssuer == nil {
			panic("cannot add block issuer key to account without BlockIssuer feature")
		}
		blockIssuer.BlockIssuerKeys = append(blockIssuer.BlockIssuerKeys, key)
	}
}

func WithBlockIssuerKeys(keys iotago.BlockIssuerKeys) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		blockIssuer := accountOutput.FeatureSet().BlockIssuer()
		if blockIssuer == nil {
			panic("cannot set block issuer keys to account without BlockIssuer feature")
		}
		blockIssuer.BlockIssuerKeys = keys
	}
}

func WithBlockIssuerExpirySlot(expirySlot iotago.SlotIndex) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		blockIssuer := accountOutput.FeatureSet().BlockIssuer()
		if blockIssuer == nil {
			panic("cannot set block issuer expiry slot to account without BlockIssuer feature")
		}
		blockIssuer.ExpirySlot = expirySlot
	}
}

func WithMana(mana uint64) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.Mana = mana
	}
}

func WithDeposit(deposit uint64) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.Amount = deposit
	}
}

func WithIncreasedStateIndex() options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.StateIndex++
	}
}

func WithIncreasedFoundryCounter(diff uint32) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.FoundryCounter += diff
	}
}

func WithFeatures(features iotago.AccountOutputFeatures) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.Features = features
	}
}

func WithImmutableFeatures(features iotago.AccountOutputImmFeatures) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.ImmutableFeatures = features
	}
}

func WithConditions(conditions iotago.AccountOutputUnlockConditions) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.Conditions = conditions
	}
}

func WithNativeTokens(nativeTokens iotago.NativeTokens) options.Option[iotago.AccountOutput] {
	return func(accountOutput *iotago.AccountOutput) {
		accountOutput.NativeTokens = nativeTokens
	}
}

// Transaction options

func WithInputs(inputs utxoledger.Outputs) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		for _, input := range inputs {
			switch input.OutputType() {
			case iotago.OutputFoundry:
				// For foundries we need to unlock the alias
				txBuilder.AddInput(&builder.TxInput{
					UnlockTarget: input.Output().UnlockConditionSet().ImmutableAccount().Address,
					InputID:      input.OutputID(),
					Input: iotago.OutputWithCreationTime{
						Output:       input.Output(),
						CreationTime: input.CreationTime(),
					},
				})
			case iotago.OutputAccount:
				// For alias we need to unlock the state controller
				txBuilder.AddInput(&builder.TxInput{
					UnlockTarget: input.Output().UnlockConditionSet().StateControllerAddress().Address,
					InputID:      input.OutputID(),
					Input: iotago.OutputWithCreationTime{
						Output:       input.Output(),
						CreationTime: input.CreationTime(),
					},
				})
			default:
				txBuilder.AddInput(&builder.TxInput{
					UnlockTarget: input.Output().UnlockConditionSet().Address().Address,
					InputID:      input.OutputID(),
					Input: iotago.OutputWithCreationTime{
						Output:       input.Output(),
						CreationTime: input.CreationTime(),
					},
				})
			}
		}
	}
}

func WithAccountInput(input *utxoledger.Output, governorTransition bool) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		switch input.OutputType() {
		case iotago.OutputAccount:
			address := input.Output().UnlockConditionSet().StateControllerAddress().Address
			if governorTransition {
				address = input.Output().UnlockConditionSet().GovernorAddress().Address
			}
			txBuilder.AddInput(&builder.TxInput{
				UnlockTarget: address,
				InputID:      input.OutputID(),
				Input: iotago.OutputWithCreationTime{
					Output:       input.Output(),
					CreationTime: input.CreationTime(),
				},
			})
		default:
			panic("only OutputAccount can be added as account input")
		}
	}
}

func WithAllotments(allotments iotago.Allotments) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		for _, allotment := range allotments {
			txBuilder.AddAllotment(allotment)
		}
	}
}

func WithCreationTime(creationTime iotago.SlotIndex) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		txBuilder.SetCreationTime(creationTime)
	}
}

func WithContextInputs(contextInputs iotago.TxEssenceContextInputs) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		for _, input := range contextInputs {
			txBuilder.AddContextInput(input)
		}
	}
}

func WithOutputs(outputs iotago.Outputs[iotago.Output]) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		for _, output := range outputs {
			txBuilder.AddOutput(output)
		}
	}
}

func WithTaggedDataPayload(payload *iotago.TaggedData) options.Option[builder.TransactionBuilder] {
	return func(txBuilder *builder.TransactionBuilder) {
		txBuilder.AddTaggedDataPayload(payload)
	}
}
