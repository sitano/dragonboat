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

	"github.com/lni/dragonboat/v3/config"
	"github.com/lni/dragonboat/v3/internal/fileutil"
	"github.com/lni/dragonboat/v3/internal/logdb"
	"github.com/lni/dragonboat/v3/internal/logdb/kv/pebble"
	"github.com/lni/dragonboat/v3/internal/vfs"
	"github.com/lni/dragonboat/v3/raftpb"
	"github.com/lni/dragonboat/v3/tools/logdump/proto"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	if len(os.Args) >= 3 {
		arg := os.Args[2]
		if arg == "-h" || arg == "--help" || arg == "help" {
			switch cmd {
			case "describe", "scan":
				printUsage()
				return
			}
		}
	}

	switch cmd {
	case "describe":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "error: data directory is required\n\n")
			printUsage()
			os.Exit(1)
		}
		runDescribe(os.Args[2], os.Args[3:])
	case "scan":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "error: data directory is required\n\n")
			printUsage()
			os.Exit(1)
		}
		runScan(os.Args[2], os.Args[3:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func runDescribe(dir string, flagArgs []string) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "error: invalid data directory %q\n", dir)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("describe", flag.ExitOnError)
	cluster := fs.Uint64("cluster", 0, "Filter by cluster ID (optional).")
	node := fs.Uint64("node", 0, "Filter by node ID (optional).")
	fs.Parse(flagArgs)

	s := raftpb.RaftDataStatus{}
	if err := fileutil.GetFlagFileContent(dir, flagFilename, &s, vfs.DefaultFS); err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path.Join(dir, flagFilename), err)
	}

	fmt.Println("MANIFEST:", ManifestString(&s))

	db, err := logdb.OpenReadOnlyLogDB(config.GetDefaultLogDBConfig(),
		dir, path.Join(dir, "wal"),
		vfs.DefaultFS, pebble.PebbleReadOnlyNoLockKVStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}

	d := NewDumper(db)
	defer d.Close()

	summaries, err := d.Describe(*cluster, *node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error describing nodes: %v\n", err)
		os.Exit(1)
	}

	PrintSummary(summaries)
}

func runScan(dir string, flagArgs []string) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "error: invalid data directory %q\n", dir)
		os.Exit(1)
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

	fs.Parse(flagArgs)

	if *decodeEntryCmd {
		*decodeEntryHeader = true
	}

	if *cluster == 0 {
		fmt.Fprintf(os.Stderr, "error: --cluster is required and must be > 0\n")
		os.Exit(1)
	}
	if *node == 0 {
		fmt.Fprintf(os.Stderr, "error: --node is required and must be > 0\n")
		os.Exit(1)
	}

	if *format != "table" && *format != "json" {
		fmt.Fprintf(os.Stderr, "error: --format must be \"table\" or \"json\", got %q\n", *format)
		os.Exit(1)
	}

	// --index is a shortcut for --from N --to N+1.
	if *index > 0 {
		*from = *index
		*to = *index + 1
	}

	db, err := logdb.OpenReadOnlyLogDB(config.GetDefaultLogDBConfig(),
		dir, path.Join(dir, "wal"),
		vfs.DefaultFS, pebble.PebbleReadOnlyNoLockKVStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}

	d := NewDumper(db)
	defer d.Close()

	result, err := d.Scan(*cluster, *node, *from, *to, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting entries: %v\n", err)
		os.Exit(1)
	}

	var opts []proto.DecodeOption
	if *decodeEntryHeader {
		opts = append(opts, proto.WithEntryHeaderDecode())
	}
	if *decodeEntryCmd {
		opts = append(opts, proto.WithEntryCmdDecode())
	}

	switch *format {
	case "json":
		PrintScanJSON(result, *cluster, *node, opts...)
	default:
		PrintScanTable(result, *cluster, *node, opts...)
	}
}

func printUsage() {
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
