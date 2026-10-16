# Linux resource monitoring

`internal/modules/monitor/` owns one sampler shared by all HTTP and future MCP clients. Collection uses Linux files and `statfs`; it does not start a monitoring daemon or invoke a shell. The interval defaults to two seconds and can be configured from one to ten seconds.

## Data and limits

| Data | Source and behavior |
| --- | --- |
| Host identity | Hostname, `/etc/os-release`, kernel release, CPU model, and executable architecture; identity is cached at startup |
| CPU | `/proc/stat`; aggregate and per-core deltas, excluding duplicated guest columns; no assumed USER_HZ value |
| Memory | `/proc/meminfo`; used memory is total minus available; cache, buffers and swap are separate values |
| Uptime / load | `/proc/uptime` and `/proc/loadavg` |
| Local filesystem usage | `/proc/self/mountinfo` plus `statfs`; deduplicate bind mounts by device/filesystem and show up to 32 readable filesystems |
| Disk I/O | `/proc/diskstats`, filtered by `/sys/block` to whole devices; sectors become bytes using Linux's fixed 512 byte counter unit |
| Network | `/proc/net/dev`; receive/send byte deltas per interface; history totals exclude loopback |
| Sensors | Up to 16 readable thermal-zone temperatures from `/sys/class/thermal`; absent sensors remain absent |
| Processes | Up to 4096 numeric `/proc` entries; read `stat` only; retain PID, name, state, CPU, resident memory, threads and nice value |

Network and process visibility follow the server's Linux namespaces and permissions. CPU/memory counters may have a wider host scope. This application does not attempt to escape namespaces or read process environments/command arguments. It skips processes that vanish or cannot be read during collection, and reports when its scan limit is reached.

Remote filesystems and pseudo filesystems are excluded from usage collection, avoiding a network filesystem `statfs` call in the sampling loop. Device-mapper/RAID devices may appear alongside their physical devices in the I/O list; there is no aggregate disk throughput value that could count their shared traffic twice.

Keep at most 60 history points within the current 60-interval time window. History stores aggregate CPU, memory percentage, and network rates; it does not retain old process lists or per-core arrays. One current process list is shared by clients. `monitor_processes` applies filtering and ordering before returning at most 500 rows (default 100).

## Rates and missing data

The first sample has current totals but null CPU and throughput rates. A new or reset counter also produces a null rate until a new baseline exists. A decreasing iowait component is clamped independently because Linux documents that it can decrease; other decreasing CPU counters reset that measurement. Process CPU uses a ratio of process jiffies to aggregate jiffies and counts 100% for each logical core. Compare PID **and** process start time to reject a reused PID's previous counters.

Missing optional files produce notices or null fields. Failure to read essential CPU/memory data preserves the last successful snapshot and exposes `lastError`; its timestamp stays unchanged. The UI labels stale data, renders gaps in charts, and never substitutes a made-up value for unavailable rates or sensors. A backward clock change clears history and resets time-based rates. Returning samples after a long gap removes old history outside the current time window.

## UI and transport

`monitor_snapshot` returns the cached resource snapshot and history. `monitor_processes` accepts `sort` (`cpu`, `memory`, `pid`, `name`), a case insensitive name/PID `filter`, and a row `limit`. Both tools are read-only and pass through the same authenticated registry as other operations.

The Svelte dashboard polls these cached tools, pauses automatic polling when the document is hidden or the user selects Pause, and permits explicit refresh/search while paused. It uses native SVG charts and CSS meters. On mobile, panels stack, navigation becomes a drawer, and the process table preserves name/PID, CPU and memory while hiding secondary columns.

## Verification and source references

Go fixtures cover initial samples, counter resets, decreasing iowait, guest accounting, process-name parsing, PID reuse, vanished processes, missing/non-finite sensors and load, bind-mount deduplication, sector units, ordering/filtering, bounded history, time gaps and clock regression. A real Linux sampler test verifies advancing samples and concurrent readers. An authenticated in-process HTTP test reads actual Linux counters through the assembled application.

The measurements follow the kernel's [proc documentation](https://www.kernel.org/doc/html/latest/filesystems/proc.html), [I/O counter documentation](https://www.kernel.org/doc/html/latest/admin-guide/iostats.html), and [block counter units](https://www.kernel.org/doc/html/latest/block/stat.html). No quoted material or host configuration is copied into fixtures.

Live TCP serving and browser rendering remain unverified in the development sandbox. TCP creation is denied, and Chromium exits on a denied `setsockopt` call. Type checks and asset/HTTP tests do not prove visual layout; desktop and mobile browser checks remain a final acceptance gate in the [plan](../plans/implementation.md).
