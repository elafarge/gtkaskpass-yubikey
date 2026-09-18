{ pkgs, self, package }:
pkgs.testers.runNixOSTest {
  name = "ssh-askpass-fido-service";
  nodes.machine = { ... }: {
    imports = [ self.nixosModules.default ];
    services.ssh-askpass-fido = { enable = true; inherit package; cacheTTL = "2s"; trace = true; };
    services.dbus.enable = true;
    boot.kernelModules = [ "uhid" ];
    systemd.user.targets.test-desktop = {
      description = "Test graphical session owner";
      requires = [ "graphical-session.target" ];
    };
    users.users.alice = { isNormalUser = true; uid = 1000; linger = true; };
    environment.systemPackages = [ pkgs.python3 ];
    virtualisation.memorySize = 1024;
  };
  testScript = ''
    import shlex
    import time

    machine.start()
    machine.wait_for_unit("multi-user.target")
    machine.wait_for_unit("user@1000.service")
    def user(command):
        return "runuser -u alice -- env XDG_RUNTIME_DIR=/run/user/1000 DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus sh -c " + shlex.quote(command)
    def client(mode):
        machine.succeed(user("python ${../tests/nixos/cache_client.py} " + mode))

    machine.fail(user("systemctl --user is-active ssh-askpass-fido.service"))
    # Graphical-session startup, before any askpass or cache request.
    machine.succeed(user("systemctl --user start test-desktop.target"))
    machine.wait_until_succeeds(user("systemctl --user is-active ssh-askpass-fido.service"))
    machine.wait_until_succeeds("test -S /run/user/1000/ssh-askpass-fido/cache.sock")
    machine.succeed(user("echo fixture > /home/alice/key"))
    client("store")
    machine.succeed(user("systemctl --user is-active ssh-askpass-fido.service"))
    client("hit")
    time.sleep(2.1)
    client("miss")
    client("store")
    machine.succeed(user("systemctl --user restart ssh-askpass-fido.service"))
    client("miss")
    # No socket-activation fallback: stopped service stays stopped.
    machine.succeed(user("systemctl --user stop ssh-askpass-fido.service"))
    machine.fail("test -S /run/user/1000/ssh-askpass-fido/cache.sock")
    machine.succeed(user("systemctl --user start ssh-askpass-fido.service"))
    machine.wait_until_succeeds("test -S /run/user/1000/ssh-askpass-fido/cache.sock")
    machine.succeed(user("${package}/bin/ssh-askpass-fido-service forget --all & first=$!; ${package}/bin/ssh-askpass-fido-service forget --all & second=$!; wait $first && wait $second"))
    machine.succeed(user("systemctl --user is-active ssh-askpass-fido.service"))
    assert machine.succeed("stat -c '%a:%U' /run/user/1000/ssh-askpass-fido/cache.sock").strip() == "600:alice"
    machine.succeed(user("${package}/bin/ssh-askpass-fido-service forget --all"))
    # Feed actual kernel HID input reports; never use a real token or SSH key.
    machine.succeed("python ${../tests/nixos/uhid_touch.py} >/run/uhid.log 2>&1 &")
    machine.wait_until_succeeds("test -f /run/ssh-askpass-fido-touch-fixture/ready")
    machine.wait_until_succeeds(user("journalctl --user -u ssh-askpass-fido.service --no-pager -o cat | grep -F '1 FIDO HID interface(s) watched'"))
    def emit(command):
        script = "import socket; s=socket.socket(socket.AF_UNIX); s.connect('/run/ssh-askpass-fido-touch-fixture/control'); s.sendall(" + repr(command.encode()) + "); assert s.recv(8)==b'ok'"
        machine.succeed("python -c " + shlex.quote(script))
    emit("up")
    machine.wait_until_succeeds(user("journalctl --user -u ssh-askpass-fido.service --no-pager -o cat | grep 'touch-state.*needed=true'"))
    emit("done")
    machine.wait_until_succeeds(user("journalctl --user -u ssh-askpass-fido.service --no-pager -o cat | grep 'touch-state.*needed=false'"))
    machine.fail("test -e /run/ssh-askpass-fido-touch-fixture/unexpected-write")
    emit("quit")
    machine.succeed(user("systemctl --user stop test-desktop.target"))
    machine.wait_until_succeeds(user("test \"$(systemctl --user is-active ssh-askpass-fido.service)\" = inactive"))
    machine.fail(user("systemctl --user is-active ssh-askpass-fido.service"))
  '';
}
