Hey there, 

I'd like to build an SSH askpass program that will, based on the string passed to it (e.g. "Confirm
user presence for key" by openssh), either ask for the passphrase of the SSH key or if we're having
a yubikey (prompt string should then be "Confirm user presence for key"), ask for the yubikey
passphrase and, once inputted and validated, will show a prompt for the user to touch the key and
close the prompt when the key is touched.

How we'll work together:
- I'd like you to document the design decisions in a DESIGN.md doc. Initially, only create this
document and let me review/amend it before implementing anything.
- Then you'll be fully autonomous. I entrust you to add functional tests as much as you can of course. Unit tests but also, since the ssh-askpass input and outputs are fairly normalized, integration tests to check compliance with these expectations as well

A few implementation details:
- I'd like you to code in Go (because I'm fluent in Go, no other reason)
- I'd like you to use to use the GTK4 UI toolkit 
- It only needs to run on Linux, don't worry about compatibility with other OSes
- use the go "standard layout"
- Try to chunk your work in commits of a reasonable size, make this current folder a git repo but
don't push anything to github for now
- Add a nix flake.nix to build the project, I'd like to embed it easily on my nixos installations
and maybe make it an official nixos package one day
- in all your commits, add a standard "assisted-by" annotation, precising which model is used
