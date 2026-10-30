// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

// Test-only custom resolver: real EIP-3668 failure propagation through the
// pinned Universal Resolver. No live gateway or private-network exception.
contract UnavailableCCIPResolver {
    error OffchainLookup(address sender, string[] urls, bytes callData, bytes4 callbackFunction, bytes extraData);
    function supportsInterface(bytes4 id) external pure returns (bool) {
        return id == 0x01ffc9a7 || id == 0x9061b923;
    }
    function resolve(bytes calldata, bytes calldata data) external view returns (bytes memory) {
        string[] memory urls = new string[](1);
        urls[0] = "https://unavailable.ens.invalid/{sender}/{data}.json";
        revert OffchainLookup(address(this), urls, data, this.resolveCallback.selector, "");
    }
    function resolveCallback(bytes calldata response, bytes calldata) external pure returns (bytes memory) {
        return response;
    }
}
