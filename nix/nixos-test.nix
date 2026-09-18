{ pkgs, self, package }:
pkgs.testers.runNixOSTest {
  name = "gtkaskpass-cache-service";
  nodes.machine = { ... }: {
    imports = [ self.nixosModules.default ];
    services.gtkaskpass-yubikey = { enable = true; inherit package; cacheTTL = "2s"; };
    services.dbus.enable = true;
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

    machine.wait_until_succeeds(user("systemctl --user is-active gtkaskpass-yubikey-cache.socket"))
    machine.fail(user("systemctl --user is-active gtkaskpass-yubikey-cache.service"))
    machine.succeed(user("echo fixture > /home/alice/key"))
    client("store")
    machine.succeed(user("systemctl --user is-active gtkaskpass-yubikey-cache.service"))
    client("hit")
    time.sleep(2.1)
    client("miss")
    client("store")
    machine.succeed(user("systemctl --user restart gtkaskpass-yubikey-cache.service"))
    client("miss")
    # Concurrent clients trigger only one socket-activated daemon.
    machine.succeed(user("systemctl --user stop gtkaskpass-yubikey-cache.service"))
    machine.succeed(user("${package}/bin/gtkaskpass-yubikey-cache forget --all & first=$!; ${package}/bin/gtkaskpass-yubikey-cache forget --all & second=$!; wait $first && wait $second"))
    machine.succeed(user("systemctl --user is-active gtkaskpass-yubikey-cache.service"))
    assert machine.succeed("stat -c '%a:%U' /run/user/1000/gtkaskpass-yubikey/cache.sock").strip() == "600:alice"
    machine.succeed(user("${package}/bin/gtkaskpass-yubikey-cache forget --all"))
  '';
}
