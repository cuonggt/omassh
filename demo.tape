# Records demo.gif. Run via ./hack/demo.sh, which starts the SSH server the
# session tabs connect to. https://github.com/charmbracelet/vhs

Output demo.gif

Set Shell zsh
Set FontSize 14
Set Width 1180
Set Height 620
Set Padding 18
Set Theme "TokyoNight"
Set TypingSpeed 55ms

Hide
Type "clear && /tmp/omassh-demo/omassh -db /tmp/omassh-demo/omassh.db -secrets=memory -o UserKnownHostsFile=/tmp/omassh-demo/known_hosts" Enter
Sleep 3s
Show

Sleep 1200ms

# reachability across the selected group
Type "p"     Sleep 2200ms

# a group whose hosts inherit a user and a key
Type "1"     Sleep 600ms
Type "j"     Sleep 1200ms
Type "2"     Sleep 900ms

# saved commands, fanned out or run here
Type "S"     Sleep 2000ms
Escape       Sleep 600ms

# the headline: enter opens the session in a tab of its own
Enter        Sleep 3500ms
Type "uptime" Enter
Sleep 1800ms

# back to the list without losing the session
Ctrl+\
Type "w"     Sleep 1200ms

# a second host, a second tab
Type "j"     Sleep 500ms
Enter        Sleep 3000ms
Type "df -h /" Enter
Sleep 1800ms

# tabs switch with the prefix; the first tab is always the host list
Ctrl+\
Type "p"     Sleep 1600ms
Ctrl+\
Type "w"     Sleep 1500ms

Type "?"     Sleep 2500ms
Escape       Sleep 500ms
Type "q"     Sleep 1000ms
