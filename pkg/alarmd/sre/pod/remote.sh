# Run by the target's /bin/sh through a non-TTY exec: $1 is the nonce, $2 the
# deadline in whole seconds, the rest the command. The command's own stdout
# and stderr pass through unchanged; this supervisor's records go to stderr
# through fd 3, each a whole line starting with the nonce: started, group
# <1 if the command leads a process group, else 0>, expired (written by the
# timer when it fires), complete <exit code>, or nosleep (no timer possible;
# the command then replaces this shell). The shell's own job notices go to
# /dev/null.
nonce=$1 t=$2
shift 2
# A command's output is read as it is written, so a timed-out command still
# answers with what it printed. Python buffers a non-TTY stdout and loses
# that buffer when the timer ends it; unbuffered, it has flushed every line.
export PYTHONUNBUFFERED=1
exec 3>&2 2>/dev/null
# The exec's stdin is kept on fd 4 for the command: a shell without job
# control gives a background command /dev/null for stdin before any
# redirection of its own (POSIX; dash applies it, bash does not), so the
# command's <&0 would read nothing and a script sent as stdin would exit 0.
exec 4<&0
if ! command -v sleep >/dev/null; then
	printf '%s nosleep\n' "$nonce" >&3
	exec "$@" 2>&3 3>&- 4<&-
fi
# The command gets a process group of its own, so the timer ends its
# children's children too: under setsid where the image has it (it makes one
# whatever the shell), else as a job under the shell's job control - which
# dash and busybox ash turn off without a tty, as an exec has none, even
# though set -m succeeds. The two are not combined: under job control the
# job already leads a group, and setsid would fork away from $!. Whether a
# group exists is checked after the launch, not assumed.
wrap=
if command -v setsid >/dev/null; then
	wrap=setsid
elif (set -m); then
	set -m
fi
# No started record, no command: a record that cannot be written ends the
# supervisor before the command is launched.
printf '%s started\n' "$nonce" >&3 || exit 125
if [ -n "$wrap" ]; then
	setsid "$@" <&4 4<&- 2>&3 3>&- &
else
	"$@" <&4 4<&- 2>&3 3>&- &
fi
pid=$!
# The timer starts before anything more is written: a write into a closed
# output pipe may end this shell, never the timer, which ends the command.
# The timer's sleeps close fd 3: one left behind would hold the exec's
# stderr, and so the stream, open.
(
	# A closed output pipe must not end the timer before its kill.
	trap '' PIPE
	# The sleep is the timer's child: a TERM to the timer ends it too, also
	# where the shell has no job control to send it to the group.
	sleep "$t" 3>&- &
	s=$!
	trap 'kill "$s"; exit 0' TERM
	wait "$s"
	printf '\n%s expired\n' "$nonce" >&3
	kill -TERM -"$pid" || kill -TERM "$pid"
	sleep 2 3>&-
	kill -KILL -"$pid" || kill -KILL "$pid"
) </dev/null >/dev/null 4<&- &
timer=$!
# setsid makes the group once it runs, a moment after the launch: wait for
# it while the command lives, a bounded number of tries.
tries=0
while ! kill -0 -"$pid" && kill -0 "$pid" && [ -n "$wrap" ] && [ "$tries" -lt 100 ]; do
	tries=$((tries + 1))
	sleep 0 3>&-
done
group=0
kill -0 -"$pid" && group=1
printf '\n%s group %s\n' "$nonce" "$group" >&3
wait "$pid"
rc=$?
# Members of the command's group still holding its output would keep the
# exec stream open: they go with it.
[ "$group" -eq 1 ] && kill -KILL -"$pid"
kill -TERM -"$timer" || kill -TERM "$timer"
wait "$timer"
printf '\n%s complete %s\n' "$nonce" "$rc" >&3
