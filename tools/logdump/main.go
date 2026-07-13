// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/lni/dragonboat/v3/tools/logdump/proto"
)

type DescribeConfig struct {
	Dir     string
	Cluster uint64
	Node    uint64
}

type ScanConfig struct {
	Dir     string
	Cluster uint64
	Node    uint64

	From  uint64
	To    uint64
	Index uint64
	Limit uint64

	Format string

	DecodeEntryHeader bool
	DecodeEntryCmd    bool

	CmdDecoder proto.CmdDecoderFunc
}

func main() {
	if len(os.Args) < 2 {
		PrintUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	if len(os.Args) >= 3 {
		arg := os.Args[2]
		if arg == "-h" || arg == "--help" || arg == "help" {
			switch cmd {
			case "describe", "scan":
				PrintUsage()
				return
			}
		}
	}

	switch cmd {
	case "describe":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "error: data directory is required\n\n")
			PrintUsage()
			os.Exit(1)
		}
		cfg, err := ParseDescribeFlags(os.Args[2], os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
			PrintUsage()
			os.Exit(1)
		}
		DescribeCmd(cfg)
	case "scan":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "error: data directory is required\n\n")
			PrintUsage()
			os.Exit(1)
		}
		cfg, err := ParseScanFlags(os.Args[2], os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
			PrintUsage()
			os.Exit(1)
		}
		ScanCmd(cfg)
	case "-h", "--help", "help":
		PrintUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		PrintUsage()
		os.Exit(1)
	}
}

func DescribeCmd(cfg *DescribeConfig) error {
	ld, err := OpenLogDumpReadOnly(cfg.Dir, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database:\n%v", err)
		os.Exit(1)
	}

	s, err := ld.GetRaftDataStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path.Join(cfg.Dir, flagFilename), err)
		os.Exit(1)
	}

	fmt.Println("MANIFEST:", ManifestString(&s))

	d := ld.Dumper()
	defer d.Close()

	summaries, err := d.Describe(cfg.Cluster, cfg.Node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error describing nodes: %v\n", err)
		os.Exit(1)
	}

	PrintSummary(summaries)

	return nil
}

func ScanCmd(cfg *ScanConfig) error {
	ld, err := OpenLogDumpReadOnly(cfg.Dir, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database:\n%v", err)
		os.Exit(1)
	}

	d := ld.Dumper()
	defer d.Close()

	result, err := d.Scan(cfg.Cluster, cfg.Node, cfg.From, cfg.To, cfg.Limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting entries: %v\n", err)
		os.Exit(1)
	}

	var opts []proto.DecodeOption
	if cfg.DecodeEntryHeader {
		opts = append(opts, proto.WithEntryHeaderDecode())
	}
	if cfg.DecodeEntryCmd {
		opts = append(opts, proto.WithEntryCmdDecode())
	}
	if cfg.CmdDecoder != nil {
		opts = append(opts, proto.WithCmdDecoder(cfg.CmdDecoder))
	}

	switch cfg.Format {
	case "json":
		PrintScanJSON(result, cfg.Cluster, cfg.Node, opts...)
	default:
		PrintScanTable(result, cfg.Cluster, cfg.Node, opts...)
	}

	return nil
}

func ParseDescribeFlags(dir string, flagArgs []string) (*DescribeConfig, error) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("invalid data directory %q", dir)
	}

	fs := flag.NewFlagSet("describe", flag.ExitOnError)
	cluster := fs.Uint64("cluster", 0, "Filter by cluster ID (optional).")
	node := fs.Uint64("node", 0, "Filter by node ID (optional).")
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}

	return &DescribeConfig{
		Dir:     dir,
		Cluster: *cluster,
		Node:    *node,
	}, nil
}

func ParseScanFlags(dir string, flagArgs []string) (*ScanConfig, error) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("invalid data directory %q", dir)
	}

	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	cluster := fs.Uint64("cluster", 0, "Cluster ID (required).")
	node := fs.Uint64("node", 0, "Node ID (required).")
	from := fs.Uint64("from", 0, "Start index (inclusive, default: first available).")
	to := fs.Uint64("to", 0, "End index (exclusive, default: last+1).")
	index := fs.Uint64("index", 0, "Single entry shortcut (--from N --to N+1).")
	limit := fs.Uint64("limit", 100, "Max entries to display (default: 100, 0 = unlimited).")
	format := fs.String("format", "table", "Output format: table (default) or json.")
	decodeEntryHeader := fs.Bool("decode-entry-header", false, "Structurally decode Cmd payloads (protobuf, snappy).")
	decodeEntryCmd := fs.Bool("decode-entry-cmd", false, "Walk protobuf wire format in Cmd payloads (implies --decode-value).")

	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}

	if *decodeEntryCmd {
		*decodeEntryHeader = true
	}

	if *cluster == 0 {
		return nil, fmt.Errorf("--cluster is required and must be > 0")
	}
	if *node == 0 {
		return nil, fmt.Errorf("--node is required and must be > 0")
	}

	if *format != "table" && *format != "json" {
		return nil, fmt.Errorf("--format must be \"table\" or \"json\", got %q", *format)
	}

	// --index is a shortcut for --from N --to N+1.
	if *index > 0 {
		*from = *index
		*to = *index + 1
	}

	return &ScanConfig{
		Dir:               dir,
		Cluster:           *cluster,
		Node:              *node,
		From:              *from,
		To:                *to,
		Index:             *index,
		Limit:             *limit,
		Format:            *format,
		DecodeEntryHeader: *decodeEntryHeader,
		DecodeEntryCmd:    *decodeEntryCmd,
	}, nil
}

func PrintUsage() {
	fmt.Print(`logdump — Dragonboat RAFT log inspection tool

USAGE:
  logdump describe <data-dir> [flags]
  logdump scan     <data-dir> --cluster N --node N [flags]

COMMANDS:
  describe  Show available clusters, nodes, and their RAFT state.
  scan      Pretty-print RAFT log entries with metadata.

LIST FLAGS:
  --cluster N   Filter by cluster ID (optional).
  --node N      Filter by node ID (optional).

GET FLAGS:
  --cluster N   Cluster ID (required).
  --node N      Node ID (required).

  --from N      Start index (inclusive, default: first available).
  --to N        End index (exclusive, default: last+1).
  --index N     Single entry shortcut (--from N --to N+1).
  --limit N     Max entries to display (default: 100, 0 = unlimited).

  --format fmt  Output format: table (default) or json.

  --decode-entry-header Structurally decode Cmd payloads (raftpb.Entry).
  --decode-entry-cmd Walk protobuf wire format in Cmd payloads (implies --decode-entry-header).

EXAMPLES:
  logdump describe /var/dragonboat/nodehost
  logdump describe /var/dragonboat/nodehost --cluster 1
  logdump scan  /var/dragonboat/nodehost --cluster 1 --node 2 --from 100 --to 200
  logdump scan  /var/dragonboat/nodehost --cluster 1 --node 2 --index 42
  logdump scan  /var/dragonboat/nodehost --cluster 1 --node 2 --format json
  logdump scan  /var/dragonboat/nodehost --cluster 1 --node 2 --index 42 --format json --decode-entry-header --decode-entry-cmd
`)
}
