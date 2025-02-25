package runner

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/cpurta/eth-scanner/cmd/internal/block"
	"github.com/cpurta/eth-scanner/cmd/internal/transaction"
	"github.com/urfave/cli"
)

type EthereumTransactionScannerRunner struct {
	endpoint       string
	blockWorkerNum int
	startBlock     int64
	endBlock       int64
	filterAddress  string

	blockWorkerManager  *block.BlockManager
	blockWorkers        []*block.BlockWorker
	transactionWorker   *transaction.TransactionWorker
	transactionReporter *transaction.TransactionReporter

	rawTransactions      chan *transaction.TransactionResult
	filteredTransactions chan *transaction.TransactionResult

	outputfile *excelize.File

	filePath string

	waitGroup *sync.WaitGroup

	sigKillChan chan os.Signal
	done        bool
}

func NewCommand(sigKillChan chan os.Signal) cli.Command {
	runner := &EthereumTransactionScannerRunner{
		rawTransactions:      make(chan *transaction.TransactionResult, 10000),
		filteredTransactions: make(chan *transaction.TransactionResult, 1000),
		waitGroup:            &sync.WaitGroup{},
		sigKillChan:          sigKillChan,
		done:                 false,
	}

	return cli.Command{
		Name:   "start",
		Usage:  "Scan all blocks on the ethereum block chain for all transactions using a specific address",
		Action: runner.start,
		Flags:  runner.getStartFlags(),
	}
}

func (runner *EthereumTransactionScannerRunner) initialize(c *cli.Context) error {
	runner.filePath = fmt.Sprintf("%s-transactions-%s", runner.filterAddress, time.Now().Format("2006/1/02-15:04"))

	runner.outputfile = excelize.NewFile()

	categories := map[string]string{
		"A1": "hash", "B1": "nonce", "C1": "blockHash",
		"D1": "blockNumber", "E1": "transactionIndex", "F1": "from",
		"G1": "to", "H1": "value", "I1": "gas",
		"J1": "gasPrice", "K1": "input", "L1": "raw", "M1": "isContract"}

	for k, v := range categories {
		runner.outputfile.SetCellValue("Sheet1", k, v)
	}

	workers := make([]*block.BlockWorker, 0)
	for i := 0; i < runner.blockWorkerNum; i++ {
		workers = append(workers, block.NewBlockWorker(runner.endpoint, runner.rawTransactions, runner.waitGroup))
	}

	log.Println("Starting scanner for blocks", runner.startBlock, "-", runner.endBlock)

	manager := block.NewBlockManager(workers, block.NewBlockRange(runner.startBlock, runner.endBlock), runner.waitGroup)

	transactionWorker := transaction.NewTransactionWorker(runner.rawTransactions, runner.filteredTransactions, runner.filterAddress, runner.waitGroup)

	transactionReporter := transaction.NewTransactionReporter(runner.outputfile, runner.filteredTransactions, runner.waitGroup)

	runner.blockWorkers = workers
	runner.blockWorkerManager = manager
	runner.transactionWorker = transactionWorker
	runner.transactionReporter = transactionReporter

	return nil
}

func (runner *EthereumTransactionScannerRunner) start(c *cli.Context) error {
	if err := runner.initialize(c); err != nil {
		return err
	}

	log.Println("starting block workers manager...")
	runner.waitGroup.Add(1)
	go runner.blockWorkerManager.StartWorkers()

	log.Println("starting transaction worker...")
	runner.waitGroup.Add(1)
	go runner.transactionWorker.Start()

	log.Println("starting to log all transactions...")
	runner.waitGroup.Add(1)
	go runner.transactionReporter.Start()

	log.Println("starting scanner reporter...")
	runner.waitGroup.Add(1)
	go runner.reportProgress()

	runner.waitGroup.Add(1)
	go runner.handleShutdown()

	runner.waitGroup.Wait()

	return nil
}
