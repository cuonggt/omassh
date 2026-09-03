# A VHS tape for the README demo: vhs demo.tape
# Expects a seeded database at /tmp/omassh-demo.db (see hack/seed).
# https://github.com/charmbracelet/vhs

Output demo.gif

Set Shell zsh
Set FontSize 14
Set Width 1180
Set Height 600
Set Padding 18
Set Theme "TokyoNight"
Set TypingSpeed 60ms

Hide
Type "clear && /tmp/omassh-demo -db /tmp/omassh-demo.db -secrets=memory" Enter
Sleep 3s
Show

Sleep 1500ms

# reachability: one host up, the rest unreachable
Type "p"    Sleep 2500ms

# jump to Production, whose hosts inherit a user and a bastion from the group
Type "1"    Sleep 700ms
Type "j"    Sleep 1400ms
Type "2"    Sleep 700ms
Type "j"    Sleep 1000ms
Type "j"    Sleep 1600ms

# port forwards attached to this host
Type "3"    Sleep 1800ms
Type "2"    Sleep 600ms

# fuzzy search reaches every host, not just this group
Type "/"    Sleep 500ms
Type "web"  Sleep 2000ms
Escape      Sleep 900ms

# saved commands
Type "S"    Sleep 2200ms
Escape      Sleep 700ms

# the cheatsheet
Type "?"    Sleep 3000ms
Escape      Sleep 800ms

Type "q"    Sleep 1200ms
