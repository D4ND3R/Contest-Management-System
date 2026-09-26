#!/usr/bin/env bash
# Verifies that this machine judges a contest correctly: kernel, control
# groups, isolate (installation, permissions, a real sandboxed run), cores,
# SMT / turbo / frequency scaling, swap and clock, then judges the security
# battery and the sample solutions TWICE through the real sandbox and
# requires identical verdicts ("cms ctl judge-selftest").
#
# Do NOT start a contest on a host where this script reports FAIL.
#
#   sudo scripts/verify-host.sh [--config /etc/cms/cms.yaml] [--cms /usr/local/bin/cms]
#                               [--languages c11,cpp17,python3] [--runs 2]
#                               [--isolate /usr/local/bin/isolate] [--skip-judge]
#                               [--box 999] [--box-offset 500]
#
# --box is the isolate box used for the quick sandbox check and
# --box-offset the first box of the self-test; neither may be used by a
# running worker (workers use box_id_offset .. offset + 12 x cores).
#
# Exit status: 0 when every check passed (warnings allowed), 1 otherwise.
set -uo pipefail

CONFIG=${CMS_CONFIG:-}
CMS=""
LANGUAGES=""
RUNS=2
ISOLATE=""
SKIP_JUDGE=0
BOX=999
BOX_OFFSET=500

while [ $# -gt 0 ]; do
  case "$1" in
    --config) CONFIG=$2; shift 2 ;;
    --cms) CMS=$2; shift 2 ;;
    --languages) LANGUAGES=$2; shift 2 ;;
    --runs) RUNS=$2; shift 2 ;;
    --isolate) ISOLATE=$2; shift 2 ;;
    --box) BOX=$2; shift 2 ;;
    --box-offset) BOX_OFFSET=$2; shift 2 ;;
    --skip-judge) SKIP_JUDGE=1; shift ;;
    -h|--help) sed -n '2,21p' "$0"; exit 0 ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
done

FAILS=0
WARNS=0
ok()   { printf '[ OK ] %s\n' "$1"; }
info() { printf '[INFO] %s\n' "$1"; }
warn() { printf '[WARN] %s\n' "$1"; [ -n "${2:-}" ] && printf '       fix: %s\n' "$2"; WARNS=$((WARNS + 1)); }
fail() { printf '[FAIL] %s\n' "$1"; [ -n "${2:-}" ] && printf '       fix: %s\n' "$2"; FAILS=$((FAILS + 1)); }
readf() { cat "$1" 2>/dev/null | head -1; }

echo "== CMS host verification ($(hostname), $(date -u '+%Y-%m-%d %H:%M UTC')) =="

# --- privileges -----------------------------------------------------------
if [ "$(id -u)" = 0 ]; then
  ok "running as root"
else
  fail "not running as root (the sandbox checks need it)" "run: sudo $0 $*"
fi

# --- kernel ---------------------------------------------------------------
KERNEL=$(uname -r)
KMAJ=${KERNEL%%.*}; KREST=${KERNEL#*.}; KMIN=${KREST%%.*}
if [ "$KMAJ" -gt 5 ] || { [ "$KMAJ" -eq 5 ] && [ "$KMIN" -ge 4 ]; }; then
  ok "kernel $KERNEL"
else
  fail "kernel $KERNEL is too old for isolate with cgroup v2" "use a distribution with kernel >= 5.4 (Ubuntu 22.04+, Debian 12+)"
fi

# --- isolate --------------------------------------------------------------
[ -z "$ISOLATE" ] && ISOLATE=$(command -v isolate || true)
ISO_MAJOR=0
if [ -z "$ISOLATE" ] || [ ! -x "$ISOLATE" ]; then
  fail "isolate is not installed (${ISOLATE:-not in PATH})" "sudo scripts/install-isolate.sh"
else
  ISO_VERSION=$("$ISOLATE" --version 2>/dev/null | head -1 | sed 's/.*isolator //')
  ISO_MAJOR=${ISO_VERSION%%.*}
  case "$ISO_MAJOR" in ''|*[!0-9]*) ISO_MAJOR=0 ;; esac
  ok "isolate $ISO_VERSION at $ISOLATE"
  OWNER=$(stat -c '%U' "$ISOLATE"); MODE=$(stat -c '%a' "$ISOLATE")
  if [ "$OWNER" = root ] && [ "${#MODE}" = 4 ] && [ "${MODE:0:1}" -ge 4 ]; then
    ok "isolate is setuid root (mode $MODE)"
  else
    fail "isolate is not setuid root (owner $OWNER, mode $MODE): workers not running as root cannot use it" \
      "sudo chown root:root $ISOLATE && sudo chmod 4755 $ISOLATE"
  fi
fi
ISO_CONF=""
for c in /usr/local/etc/isolate /etc/isolate; do [ -f "$c" ] && { ISO_CONF=$c; break; }; done
if [ -z "$ISO_CONF" ]; then
  fail "no isolate configuration (/usr/local/etc/isolate)" "sudo scripts/install-isolate.sh (writes it)"
else
  BOX_ROOT=$(sed -n 's/^[[:space:]]*box_root[[:space:]]*=[[:space:]]*//p' "$ISO_CONF" | tail -1)
  CG_ROOT=$(sed -n 's/^[[:space:]]*cg_root[[:space:]]*=[[:space:]]*//p' "$ISO_CONF" | tail -1)
  NUM_BOXES=$(sed -n 's/^[[:space:]]*num_boxes[[:space:]]*=[[:space:]]*//p' "$ISO_CONF" | tail -1)
  ok "isolate configuration $ISO_CONF (box_root ${BOX_ROOT:-?}, cg_root ${CG_ROOT:-?}, num_boxes ${NUM_BOXES:-?})"
  if [ -n "$BOX_ROOT" ]; then
    if [ ! -d "$BOX_ROOT" ]; then
      fail "box_root $BOX_ROOT does not exist" "sudo mkdir -p $BOX_ROOT && sudo chmod 755 $BOX_ROOT"
    elif [ "$(stat -c '%U' "$BOX_ROOT")" != root ] || [ $(( 0$(stat -c '%a' "$BOX_ROOT") & 022 )) -ne 0 ]; then
      fail "box_root $BOX_ROOT must belong to root and not be group/world-writable" "sudo chown root:root $BOX_ROOT && sudo chmod 755 $BOX_ROOT"
    else
      AVAIL=$(df -Pk "$BOX_ROOT" | awk 'NR==2 {print $4}')
      if [ "${AVAIL:-0}" -lt 2097152 ]; then
        warn "only $((AVAIL / 1024)) MiB free under $BOX_ROOT" "free disk space (compilations and outputs need room)"
      else
        ok "box_root $BOX_ROOT ($((AVAIL / 1048576)) GiB free)"
      fi
    fi
  fi
  if [ -n "$NUM_BOXES" ] && [ "$NUM_BOXES" -le "$BOX" ]; then BOX=$((NUM_BOXES - 1)); fi
fi

# --- control groups -------------------------------------------------------
CGFS=$(stat -fc %T /sys/fs/cgroup 2>/dev/null)
if [ "$CGFS" = cgroup2fs ]; then
  CTRL=$(readf /sys/fs/cgroup/cgroup.controllers)
  missing=""
  for c in cpuset memory pids; do case " $CTRL " in *" $c "*) ;; *) missing="$missing $c" ;; esac; done
  if [ -z "$missing" ]; then
    ok "cgroup v2 with controllers: $CTRL"
  else
    fail "cgroup v2 lacks the controllers:$missing" "enable them in the kernel command line / systemd (Delegate=yes for isolate.service)"
  fi
  if [ "$ISO_MAJOR" -ge 2 ] && [ "${CG_ROOT#auto:}" != "${CG_ROOT:-x}" ]; then
    if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
      if systemctl is-active --quiet isolate.service; then
        ok "isolate.service (cgroup keeper) is active"
      else
        fail "isolate.service is not running (cg_root = $CG_ROOT needs it)" "sudo systemctl enable --now isolate.service"
      fi
    fi
  fi
elif [ "$ISO_MAJOR" -ge 2 ]; then
  fail "cgroup v1 hierarchy: isolate $ISO_MAJOR.x needs cgroup v2" \
    "add systemd.unified_cgroup_hierarchy=1 to GRUB_CMDLINE_LINUX in /etc/default/grub, run update-grub and reboot"
else
  warn "legacy setup: cgroup v1 with isolate 1.x (works, but deprecated)" "use isolate 2.x on a cgroup v2 host (sudo scripts/install-isolate.sh)"
fi

# --- a real sandboxed run -------------------------------------------------
if [ -n "$ISOLATE" ] && [ -x "$ISOLATE" ]; then
  "$ISOLATE" --cg --box-id="$BOX" --cleanup >/dev/null 2>&1
  if OUT=$("$ISOLATE" --cg --box-id="$BOX" --init 2>&1); then
    if RUN=$("$ISOLATE" --cg --box-id="$BOX" --cg-mem=262144 --time=1 --wall-time=3 --processes=1 --run -- /bin/true 2>&1); then
      ok "isolate --cg runs a program (box $BOX)"
    else
      fail "isolate --cg --run failed: $(echo "$RUN" | tail -1)" "check the control group setup (isolate-check-environment, isolate.service)"
    fi
    "$ISOLATE" --cg --box-id="$BOX" --cleanup >/dev/null 2>&1
  else
    fail "isolate --cg --init failed: $(echo "$OUT" | tail -1)" "check $ISO_CONF (cg_root) and that the control groups are delegated to isolate"
  fi
fi

# --- CPUs -----------------------------------------------------------------
NCPU=$(nproc --all 2>/dev/null || getconf _NPROCESSORS_ONLN)
if [ "$NCPU" -ge 2 ]; then
  ok "$NCPU CPUs (the web and database keep CPU 0; the sandbox gets its own core)"
else
  warn "only one CPU: the contest web server and the sandbox share it, and timings suffer" "use at least 2 vCPUs"
fi
SMT=$(readf /sys/devices/system/cpu/smt/active)
case "$SMT" in
  1) warn "SMT (hyperthreading) is on: a busy sibling thread slows the judging core down" \
       "keep worker.cores to one CPU per physical core, siblings idle (the installer's default since 0.3), or boot with nosmt" ;;
  0) ok "SMT (hyperthreading) is off" ;;
  *) info "SMT state not exposed (virtual machine?)" ;;
esac
if [ -f /sys/devices/system/cpu/intel_pstate/no_turbo ]; then
  if [ "$(readf /sys/devices/system/cpu/intel_pstate/no_turbo)" = 1 ]; then ok "turbo boost is off"
  else warn "turbo boost is on: running times vary with temperature and load" "sudo cms-host-tuning enable (or: echo 1 | sudo tee /sys/devices/system/cpu/intel_pstate/no_turbo)"; fi
elif [ -f /sys/devices/system/cpu/cpufreq/boost ]; then
  if [ "$(readf /sys/devices/system/cpu/cpufreq/boost)" = 0 ]; then ok "CPU boost is off"
  else warn "CPU boost is on: running times vary with temperature and load" "sudo cms-host-tuning enable (or: echo 0 | sudo tee /sys/devices/system/cpu/cpufreq/boost)"; fi
else
  info "turbo boost not exposed (virtual machine?)"
fi
GOVS=$(cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor 2>/dev/null | sort -u | tr '\n' ' ')
if [ -z "$GOVS" ]; then
  info "CPU frequency scaling not exposed (virtual machine?)"
elif [ "$GOVS" = "performance " ]; then
  ok "CPU governor: performance"
else
  warn "CPU governor: $GOVS(frequency changes make times less stable)" "sudo cms-host-tuning enable (or: sudo cpupower frequency-set -g performance)"
fi

# --- memory and clock -----------------------------------------------------
if [ "$(wc -l < /proc/swaps)" -gt 1 ]; then
  warn "swap is on: a program over its memory limit may swap (slow, unstable times) before it is stopped" "sudo swapoff -a (and remove it from /etc/fstab)"
else
  ok "no swap"
fi
if command -v timedatectl >/dev/null && [ -d /run/systemd/system ]; then
  if [ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null)" = yes ]; then ok "clock synchronised (NTP)"
  else warn "the clock is not NTP-synchronised: contest start and end times depend on it" "sudo timedatectl set-ntp true"; fi
else
  info "clock synchronisation not checked (no systemd)"
fi
if command -v isolate-check-environment >/dev/null; then
  # Its warnings (ASLR, transparent huge pages, ...) make times less stable.
  CE=$(isolate-check-environment 2>&1 | sed 's/\x1b\[[0-9;]*m//g' | grep -E '^WARNING' || true)
  if [ -z "$CE" ]; then
    ok "isolate-check-environment found nothing to improve"
  else
    warn "isolate-check-environment suggests changes for more stable times:" "sudo cms-host-tuning enable (persistent; ASLR only with ASLR=off in /etc/cms/host-tuning.conf), or sudo isolate-check-environment --execute (until the next boot)"
    echo "$CE" | sed 's/^WARNING: /         - /'
  fi
fi

# --- judging --------------------------------------------------------------
if [ "$SKIP_JUDGE" = 1 ]; then
  info "judge self-test skipped (--skip-judge)"
else
  if [ -z "$CMS" ]; then
    CMS=$(command -v cms || true)
    [ -z "$CMS" ] && [ -x "$(dirname "$0")/../bin/cms" ] && CMS="$(dirname "$0")/../bin/cms"
  fi
  if [ -z "$CMS" ] || [ ! -x "$CMS" ]; then
    fail "the cms binary was not found" "install it (make build) or pass --cms /path/to/cms"
  else
    if command -v systemctl >/dev/null && [ -d /run/systemd/system ] && systemctl is-active --quiet cms-worker 2>/dev/null; then
      warn "cms-worker is running: its jobs disturb the self-test timings" "sudo systemctl stop cms-worker while this script runs"
    fi
    echo
    echo "== judge self-test: security battery and sample solutions, $RUNS runs =="
    ARGS=(ctl judge-selftest -runs "$RUNS" -box-offset "$BOX_OFFSET")
    [ -n "$CONFIG" ] && ARGS+=(-config "$CONFIG")
    [ -n "$LANGUAGES" ] && ARGS+=(-languages "$LANGUAGES")
    if [ -n "$ISOLATE" ]; then export CMS_ISOLATE_PATH="$ISOLATE"; fi
    if "$CMS" "${ARGS[@]}"; then
      ok "judge self-test: every verdict as expected and identical in the $RUNS runs"
    else
      fail "judge self-test failed (see the list above)" \
        "a security case failing means the sandbox is not safe; a verdict changing between runs means timings are unstable (check the warnings above)"
    fi
  fi
fi

echo
if [ "$FAILS" -gt 0 ]; then
  echo "RESULT: FAIL ($FAILS failed, $WARNS warnings) — do NOT start the contest on this host."
  exit 1
fi
echo "RESULT: OK ($WARNS warnings) — this host judges correctly."
exit 0
