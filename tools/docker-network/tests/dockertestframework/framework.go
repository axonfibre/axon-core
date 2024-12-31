//go:build dockertests

package dockertestframework

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mr-tron/base58"
	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/pkg/testsuite/snapshotcreator"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/wallet"
)

var (
	// need to build snapshotfile in tools/docker-network.
	snapshotFilePath = "../docker-network-snapshots/snapshot.bin"

	keyManager = func() *wallet.KeyManager {
		genesisSeed, err := base58.Decode("7R1itJx5hVuo9w9hjg5cwKFmek4HMSoBDgJZN8hKGxih")
		if err != nil {
			log.Fatal(ierrors.Wrap(err, "failed to decode base58 seed"))
		}
		keyManager, err := wallet.NewKeyManager(genesisSeed[:], wallet.DefaultIOTAPath)
		if err != nil {
			log.Fatal(ierrors.Wrap(err, "failed to create KeyManager from seed"))
		}

		return keyManager
	}
)

type DockerTestFramework struct {
	Testing *testing.T
	// we use the fake testing so that actual tests don't fail if an assertion fails
	fakeTesting *testing.T

	nodes     map[string]*Node
	clients   map[string]mock.Client
	nodesLock syncutils.RWMutex

	snapshotPath     string
	logDirectoryPath string

	defaultWallet *mock.Wallet

	optsProtocolParameterOptions []options.Option[iotago.V3ProtocolParameters]
	optsSnapshotOptions          []options.Option[snapshotcreator.Options]
	optsWaitForSync              time.Duration
	optsWaitFor                  time.Duration
	optsTick                     time.Duration
	optsFaucetURL                string
}

func NewDockerTestFramework(t *testing.T, opts ...options.Option[DockerTestFramework]) *DockerTestFramework {
	return options.Apply(&DockerTestFramework{
		Testing:         t,
		fakeTesting:     &testing.T{},
		nodes:           make(map[string]*Node),
		clients:         make(map[string]mock.Client),
		optsWaitForSync: 5 * time.Minute,
		optsWaitFor:     2 * time.Minute,
		optsTick:        5 * time.Second,
		optsFaucetURL:   "http://localhost:8088",
	}, opts, func(d *DockerTestFramework) {
		d.optsProtocolParameterOptions = append(DefaultProtocolParametersOptions, d.optsProtocolParameterOptions...)
		protocolParams := iotago.NewV3SnapshotProtocolParameters(d.optsProtocolParameterOptions...)
		testAPI := iotago.V3API(protocolParams)

		d.logDirectoryPath = CreateLogDirectory(t.Name())
		d.snapshotPath = snapshotFilePath
		d.optsSnapshotOptions = append(DefaultAccountOptions(protocolParams),
			[]options.Option[snapshotcreator.Options]{
				snapshotcreator.WithDatabaseVersion(protocol.DatabaseVersion),
				snapshotcreator.WithFilePath(d.snapshotPath),
				snapshotcreator.WithProtocolParameters(testAPI.ProtocolParameters()),
				snapshotcreator.WithRootBlocks(map[iotago.BlockID]iotago.CommitmentID{
					testAPI.ProtocolParameters().GenesisBlockID(): iotago.NewEmptyCommitment(testAPI).MustID(),
				}),
				snapshotcreator.WithGenesisKeyManager(keyManager()),
			}...)

		err := snapshotcreator.CreateSnapshot(d.optsSnapshotOptions...)
		if err != nil {
			panic(fmt.Sprintf("failed to create snapshot: %s", err))
		}
	})
}

func (d *DockerTestFramework) DockerComposeUp(detach ...bool) error {
	cmd := exec.Command("docker", "compose", "--profile", "full", "up")

	if len(detach) > 0 && detach[0] {
		cmd = exec.Command("docker", "compose", "--profile", "full", "up", "-d")
	}

	cmd.Env = os.Environ()

	// we want to retry the candidacy much quicker in the tests, because our epochs are super short
	cmd.Env = append(cmd.Env, "CANDIDACY_RETRY_INTERVAL=1s")
	for _, node := range d.Nodes() {
		cmd.Env = append(cmd.Env, fmt.Sprintf("ISSUE_CANDIDACY_PAYLOAD_%s=%t", node.Name, node.IssueCandidacyPayload))
		if node.DatabasePath != "" {
			fmt.Println("Setting Database Path for", node.Name, " to", node.DatabasePath)
			cmd.Env = append(cmd.Env, fmt.Sprintf("DB_PATH_%s=%s", node.Name, node.DatabasePath))
		}
		if node.SnapshotPath != "" {
			fmt.Println("Setting snapshot path for", node.Name, " to", node.SnapshotPath)
			cmd.Env = append(cmd.Env, fmt.Sprintf("SNAPSHOT_PATH_%s=%s", node.Name, node.SnapshotPath))
		}
	}

	var out strings.Builder
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		fmt.Println("Docker compose up failed with error:", err, ":", out.String())
	}

	return err
}

func (d *DockerTestFramework) Run() error {
	// first we remove old containers, volumes and orphans
	_ = exec.Command("docker", "compose", "--profile", "full", "down", "-v", "--remove-orphans").Run()

	ch := make(chan error)
	stopCh := make(chan struct{})

	defer func() {
		close(stopCh)
		close(ch)
	}()

	ts := time.Now()
	go func() {
		err := d.DockerComposeUp()

		// make sure that the channel is not already closed
		select {
		case <-stopCh:
			return

		default:
			select {
			case <-stopCh:
				return
			case ch <- err:
			}
		}
	}()

	timer := time.NewTimer(d.optsWaitForSync)
	defer timer.Stop()

	ticker := time.NewTicker(d.optsTick)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-timer.C:
			require.FailNow(d.Testing, "Docker network did not start in time")
		case err := <-ch:
			if err != nil {
				require.FailNow(d.Testing, "failed to start Docker network", err)
			}
		case <-ticker.C:
			fmt.Println("Waiting for nodes to become available...")
			if d.waitForNodesOnlineAndInitClients(ts) == nil {
				break loop
			}
		}
	}

	d.GetContainersConfigs()

	// make sure all nodes are up then we can start dumping logs
	d.DumpContainerLogsToFiles()

	return nil
}

func (d *DockerTestFramework) Stop() {
	fmt.Println("Stop the network...")
	defer fmt.Println("Stop the network.....done")

	// remove volumes and orphans
	_ = exec.Command("docker", "compose", "--profile", "full", "down", "-v", "--remove-orphans").Run()
	_ = exec.Command("rm", d.snapshotPath).Run() //nolint:gosec
}

func (d *DockerTestFramework) StopContainer(containerName ...string) error {
	fmt.Println("Stopping container", containerName, "......")

	args := append([]string{"stop"}, containerName...)

	return exec.Command("docker", args...).Run()
}

func (d *DockerTestFramework) RestartContainer(containerName ...string) error {
	fmt.Println("Restarting container", containerName, "......")

	args := append([]string{"restart"}, containerName...)

	return exec.Command("docker", args...).Run()
}

func (d *DockerTestFramework) DumpContainerLogsToFiles() {
	// get container names
	cmd := "docker compose ps | awk '{print $1}' | tail -n +2"
	containerNamesBytes, err := exec.Command("bash", "-c", cmd).Output()
	require.NoError(d.Testing, err)

	// dump logs to files
	fmt.Println("Dump container logs to files...")
	containerNames := strings.Split(string(containerNamesBytes), "\n")

	for _, name := range containerNames {
		if name == "" {
			continue
		}

		d.DumpContainerLog(name)
	}
}

func (d *DockerTestFramework) DumpContainerLog(name string, optLogNameExtension ...string) {
	var filePath string
	if len(optLogNameExtension) > 0 {
		filePath = fmt.Sprintf("%s/%s-%s.log", d.logDirectoryPath, name, optLogNameExtension[0])
	} else {
		filePath = fmt.Sprintf("%s/%s.log", d.logDirectoryPath, name)
	}

	// dump logs to file if the file does not exist, which means the container is just started.
	// logs should exist for the already running containers.
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		logCmd := fmt.Sprintf("docker logs -f %s > %s 2>&1 &", name, filePath)
		err := exec.Command("bash", "-c", logCmd).Run()
		require.NoError(d.Testing, err)
	}
}

func (d *DockerTestFramework) GetContainersConfigs() {
	// get container configs
	d.nodesLock.Lock()
	defer d.nodesLock.Unlock()

	for _, node := range d.nodesWithoutLocking() {
		cmd := fmt.Sprintf("docker inspect --format='{{.Config.Cmd}}' %s", node.ContainerName)
		containerConfigsBytes, err := exec.Command("bash", "-c", cmd).Output()
		require.NoError(d.Testing, err)

		configs := string(containerConfigsBytes)
		// remove "[" and "]"
		configs = configs[1 : len(configs)-2]

		// get validator private key
		cmd = fmt.Sprintf("docker inspect --format='{{.Config.Env}}' %s", node.ContainerName)
		envBytes, err := exec.Command("bash", "-c", cmd).Output()
		require.NoError(d.Testing, err)

		envs := string(envBytes)
		envs = strings.Split(envs[1:len(envs)-2], " ")[0]

		node.ContainerConfigs = configs
		node.PrivateKey = envs

		d.nodes[node.Name] = node
	}
}

func (d *DockerTestFramework) DefaultWallet() *mock.Wallet {
	return d.defaultWallet
}

func (d *DockerTestFramework) Clients(names ...string) map[string]mock.Client {
	d.nodesLock.RLock()
	defer d.nodesLock.RUnlock()

	if len(names) == 0 {
		return d.clients
	}

	clients := make(map[string]mock.Client, len(names))
	for _, name := range names {
		client, exist := d.clients[name]
		require.True(d.Testing, exist)

		clients[name] = client
	}

	return clients
}

func (d *DockerTestFramework) Client(name string) mock.Client {
	d.nodesLock.RLock()
	defer d.nodesLock.RUnlock()

	client, exist := d.clients[name]
	require.True(d.Testing, exist)

	return client
}
