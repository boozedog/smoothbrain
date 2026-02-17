{
  description = "smoothbrain - personal infrastructure orchestrator";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "smoothbrain";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
          subPackages = [ "cmd/smoothbrain" ];

          meta = {
            description = "Personal infrastructure orchestrator";
            mainProgram = "smoothbrain";
          };
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [ go gopls gotools ];
        };
      }
    ) // {
      nixosModules.default = import ./nix/module.nix self;
    };
}
