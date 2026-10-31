// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;
import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {ERC721URIStorage} from "@openzeppelin/contracts/token/ERC721/extensions/ERC721URIStorage.sol";
import {ERC1155} from "@openzeppelin/contracts/token/ERC1155/ERC1155.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {TransparentUpgradeableProxy} from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";
import {BeaconProxy} from "@openzeppelin/contracts/proxy/beacon/BeaconProxy.sol";
import {UpgradeableBeacon} from "@openzeppelin/contracts/proxy/beacon/UpgradeableBeacon.sol";

// Deliberately permissive disposable Preview fixtures; never production contracts.
contract ToolCalls {
    uint256 public value;
    event Changed(address indexed caller, uint256 value);
    error ToolFailure(uint256 value);
    function setValue(uint256 next) external { value = next; emit Changed(msg.sender, next); }
    function fail() external pure { revert ToolFailure(7702); }
    function forward(address payable to) external payable { (bool ok,) = to.call{value: msg.value}(""); require(ok); }
    receive() external payable { emit Changed(msg.sender, msg.value); }
}
contract ToolCallsV2 is ToolCalls { function version() external pure returns (uint256) { return 2; } }
contract ToolERC20 is ERC20 {
    constructor() ERC20("Preview Tool Token", "TOOL") {}
    function mint(address to, uint256 n) external { _mint(to, n); }
    function burn(uint256 n) external { _burn(msg.sender, n); }
}
contract ToolERC721 is ERC721URIStorage {
    constructor() ERC721("Preview Tool NFT", "TOOL721") {}
    function mint(address to, uint256 id, string calldata uri_) external { _mint(to, id); _setTokenURI(id, uri_); }
    function setURI(uint256 id, string calldata uri_) external { _setTokenURI(id, uri_); }
    function burn(uint256 id) external { require(ownerOf(id) == msg.sender); _burn(id); }
}
contract ToolERC1155 is ERC1155 {
    constructor() ERC1155("") {}
    function mint(address to, uint256 id, uint256 n) external { _mint(to, id, n, ""); }
    function burn(uint256 id, uint256 n) external { _burn(msg.sender, id, n); }
    function setURI(string calldata uri_) external { _setURI(uri_); }
}
contract ToolDiamondFacet {
    bytes32 constant SLOT = keccak256("etherview.preview.tools.diamond");
    function value() external view returns (uint256 v) { bytes32 s = SLOT; assembly { v := sload(s) } }
    function setValue(uint256 v) external { bytes32 s = SLOT; assembly { sstore(s, v) } }
}
contract ToolDiamondFacetV2 {
    bytes32 constant SLOT = keccak256("etherview.preview.tools.diamond");
    function value() external view returns (uint256 v) { bytes32 s = SLOT; assembly { v := sload(s) } return v + 1000; }
    function double(uint256 v) external pure returns (uint256) { return v * 2; }
}
contract ToolDiamond {
    struct Facet { address facetAddress; bytes4[] functionSelectors; }
    struct FacetCut { address facetAddress; uint8 action; bytes4[] functionSelectors; }
    event DiamondCut(FacetCut[] diamondCut, address init, bytes data);
    mapping(bytes4 => address) private routes;
    bytes4[] private selectors;
    address public immutable owner;
    constructor(address facet) {
        owner = msg.sender;
        selectors.push(ToolDiamondFacet.value.selector);
        selectors.push(ToolDiamondFacet.setValue.selector);
        routes[selectors[0]] = facet; routes[selectors[1]] = facet;
        FacetCut[] memory cuts = new FacetCut[](1);
        cuts[0] = FacetCut(facet, 0, selectors);
        emit DiamondCut(cuts, address(0), "");
        bytes4[7] memory direct = [ToolDiamond.facets.selector, bytes4(keccak256("facetAddresses()")), bytes4(keccak256("facetFunctionSelectors(address)")), ToolDiamond.facetAddress.selector, ToolDiamond.supportsInterface.selector, ToolDiamond.diamondCut.selector, bytes4(keccak256("owner()"))];
        for(uint256 i; i < direct.length; i++) { selectors.push(direct[i]); routes[direct[i]] = address(this); }
    }
    function diamondCut(FacetCut[] calldata cuts, address init, bytes calldata data) external {
        require(msg.sender == owner && init == address(0) && data.length == 0);
        for (uint256 i; i < cuts.length; i++) {
            FacetCut calldata c = cuts[i];
            require(c.action <= 2);
            require(c.action == 2 ? c.facetAddress == address(0) : c.facetAddress.code.length > 0);
            for (uint256 j; j < c.functionSelectors.length; j++) {
                bytes4 s = c.functionSelectors[j];
                require(routes[s] != address(this), "immutable selector");
                if (c.action == 0) { require(routes[s] == address(0)); selectors.push(s); }
                else { require(routes[s] != address(0)); }
                if (c.action == 2) {
                    for (uint256 k; k < selectors.length; k++) if (selectors[k] == s) {
                        selectors[k] = selectors[selectors.length-1]; selectors.pop(); break;
                    }
                }
                routes[s] = c.facetAddress;
            }
        }
        emit DiamondCut(cuts, init, data);
    }
    function facetAddress(bytes4 s) external view returns(address) { return routes[s]; }
    function facetAddresses() public view returns(address[] memory out) {
        address[] memory temp = new address[](selectors.length); uint256 n;
        for(uint256 i; i < selectors.length; i++) {
            address f = routes[selectors[i]]; bool found;
            for(uint256 j; j < n; j++) if(temp[j] == f) found = true;
            if(!found) temp[n++] = f;
        }
        out = new address[](n); for(uint256 i; i < n; i++) out[i] = temp[i];
    }
    function facetFunctionSelectors(address f) public view returns(bytes4[] memory out) {
        uint256 n; for(uint256 i; i < selectors.length; i++) if(routes[selectors[i]] == f) n++;
        out = new bytes4[](n); n = 0;
        for(uint256 i; i < selectors.length; i++) if(routes[selectors[i]] == f) out[n++] = selectors[i];
    }
    function facets() external view returns(Facet[] memory out) {
        address[] memory fs = facetAddresses(); out = new Facet[](fs.length);
        for(uint256 i; i < fs.length; i++) out[i] = Facet(fs[i], facetFunctionSelectors(fs[i]));
    }
    function supportsInterface(bytes4 id) external pure returns(bool) { return id == 0x01ffc9a7 || id == 0x48e2b093; }
    receive() external payable {}
    fallback() external payable {
        address f = routes[msg.sig]; require(f != address(0), "missing selector");
        assembly {
            calldatacopy(0,0,calldatasize())
            let ok := delegatecall(gas(),f,0,calldatasize(),0,0)
            returndatacopy(0,0,returndatasize())
            switch ok case 0 { revert(0,returndatasize()) } default { return(0,returndatasize()) }
        }
    }
}

contract ToolUnverified {
    function marker() external pure returns(uint256) { return 123456789; }
}

// Generic ERC-1967 upgrade path without an ERC-1822 proxiableUUID probe.
contract ToolERC1967Implementation {
    address public owner;
    uint256 public value;
    event Upgraded(address indexed implementation);
    function initialize(address owner_, uint256 value_) external { require(owner == address(0)); owner = owner_; value = value_; }
    function setValue(uint256 next) external { value = next; }
    function upgradeToAndCall(address target, bytes calldata call_) external {
        require(msg.sender == owner && target.code.length > 0);
        bytes32 slot = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;
        assembly { sstore(slot, target) }
        emit Upgraded(target);
        if(call_.length > 0) { (bool ok,) = target.delegatecall(call_); require(ok); }
    }
}
contract ToolERC1967ImplementationV2 is ToolERC1967Implementation {
    function version() external pure returns(uint256) { return 2; }
}
