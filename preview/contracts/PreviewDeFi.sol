// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

// Local demonstrations of adapter accounting, NOT upstream protocol implementations.
// Public minting is intentional on disposable Preview. No real funds are accepted.
contract PreviewDeFiToken {
    string public name;
    string public symbol;
    uint8 public constant decimals = 18;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    event Transfer(address indexed from, address indexed to, uint256 value);

    constructor(string memory label) {
        name = label;
        symbol = label;
    }

    function mint(address to, uint256 value) public {
        require(to != address(0));
        balanceOf[to] += value;
        totalSupply += value;
        emit Transfer(address(0), to, value);
    }
}

contract PreviewDeFiV2Factory {
    address public pair;
    address private immutable creator = msg.sender;

    function initialize(address value) external {
        require(msg.sender == creator && pair == address(0));
        pair = value;
    }

    function allPairsLength() external pure returns (uint256) {
        return 1;
    }

    function allPairs(uint256 i) external view returns (address) {
        require(i == 0);
        return pair;
    }

    function getPair(address a, address b) external view returns (address) {
        PreviewDeFiV2Pair p = PreviewDeFiV2Pair(pair);
        return a == p.token0() && b == p.token1() ? pair : address(0);
    }
}

contract PreviewDeFiV2Pair is PreviewDeFiToken {
    address public immutable factory;
    address public immutable token0;
    address public immutable token1;

    constructor(address f, address a, address b) PreviewDeFiToken("Preview V2 LP") {
        factory = f;
        token0 = a;
        token1 = b;
    }

    function getReserves() external view returns (uint112, uint112, uint32) {
        require(totalSupply <= type(uint112).max / 3);
        return (uint112(totalSupply * 2), uint112(totalSupply * 3), 0);
    }
}

contract PreviewDeFiNFT {
    string public constant name = "Preview DeFi position";
    string public constant symbol = "PREVIEW-POS";
    mapping(uint256 => address) private owners;
    mapping(address => uint256) public balanceOf;
    mapping(uint256 => address) public issuer;
    event Transfer(address indexed from, address indexed to, uint256 indexed tokenId);

    function ownerOf(uint256 id) public view returns (address) {
        require(owners[id] != address(0));
        return owners[id];
    }

    function mint(address to, uint256 id) public virtual {
        require(to != address(0) && owners[id] == address(0));
        owners[id] = to;
        issuer[id] = msg.sender;
        balanceOf[to]++;
        emit Transfer(address(0), to, id);
    }
}

contract PreviewDeFiV3Pool {
    function slot0() external pure returns (uint160, int24, uint16, uint16, uint16, uint8, bool) {
        return (uint160(1 << 96), 0, 0, 0, 0, 0, true);
    }

    function ticks(int24) external pure returns (uint128, int128, uint256, uint256, int56, uint160, uint32, bool) {
        return (0, 0, 0, 0, 0, 0, 0, true);
    }

    function feeGrowthGlobal0X128() external pure returns (uint256) {
        return 0;
    }

    function feeGrowthGlobal1X128() external pure returns (uint256) {
        return 0;
    }
}

contract PreviewDeFiV3Factory {
    address public immutable pool;
    address private immutable token0;
    address private immutable token1;

    constructor(address p, address a, address b) {
        pool = p;
        token0 = a;
        token1 = b;
    }

    function getPool(address a, address b, uint24 fee) external view returns (address) {
        return a == token0 && b == token1 && fee == 3000 ? pool : address(0);
    }
}

contract PreviewDeFiV3Positions is PreviewDeFiNFT {
    address public immutable factory;
    address private immutable token0;
    address private immutable token1;

    constructor(address f, address a, address b) {
        factory = f;
        token0 = a;
        token1 = b;
    }

    function positions(uint256 id)
        external
        view
        returns (uint96, address, address, address, uint24, int24, int24, uint128, uint256, uint256, uint128, uint128)
    {
        ownerOf(id);
        return (0, address(0), token0, token1, 3000, 0, 1, 1e24, 0, 0, 1e17, 2e17);
    }
}

contract PreviewDeFiV4Manager {
    function previewOnly() external pure returns (bool) {
        return true;
    }
}

contract PreviewDeFiV4Positions is PreviewDeFiNFT {
    address public immutable poolManager;
    address private immutable token0;
    address private immutable token1;

    constructor(address m, address a, address b) {
        poolManager = m;
        token0 = a;
        token1 = b;
    }

    function getPoolAndPositionInfo(uint256 id)
        external
        view
        returns (address, address, uint24, int24, address, uint256)
    {
        ownerOf(id);
        return (token0, token1, 3000, 1, address(0), uint256(1) << 32);
    }
}

contract PreviewDeFiV4View {
    address public immutable poolManager;

    constructor(address m) {
        poolManager = m;
    }

    function getSlot0(bytes32) external pure returns (uint160, int24, uint24, uint24) {
        return (uint160(1 << 96), 0, 0, 3000);
    }

    function getPositionInfo(bytes32, bytes32) external pure returns (uint128, uint256, uint256) {
        return (1e24, 0, 0);
    }

    function getFeeGrowthInside(bytes32, int24, int24) external pure returns (uint256, uint256) {
        return (0, 0);
    }
}

contract PreviewDeFiAavePool {
    address private immutable underlying;

    constructor(address a) {
        underlying = a;
    }

    function getReservesList() external view returns (address[] memory a) {
        a = new address[](1);
        a[0] = underlying;
    }
}

contract PreviewDeFiReceipt is PreviewDeFiToken {
    address public immutable POOL;
    address public immutable UNDERLYING_ASSET_ADDRESS;

    constructor(address p, address a, string memory label) PreviewDeFiToken(label) {
        POOL = p;
        UNDERLYING_ASSET_ADDRESS = a;
    }
}

contract PreviewDeFiAaveProvider {
    address private immutable underlying;
    address private immutable supply;
    address private immutable debt;

    constructor(address a, address s, address d) {
        underlying = a;
        supply = s;
        debt = d;
    }

    function getReserveTokensAddresses(address a) external view returns (address, address, address) {
        require(a == underlying);
        return (supply, address(0), debt);
    }

    function getUserReserveData(address a, address owner)
        external
        view
        returns (uint256, uint256, uint256, uint256, uint256, uint256, uint256, uint40, bool)
    {
        require(a == underlying);
        uint256 supplied = PreviewDeFiToken(supply).balanceOf(owner);
        uint256 borrowed = PreviewDeFiToken(debt).balanceOf(owner);
        return (supplied, 0, borrowed, 0, 0, 0, 0, 0, true);
    }
}

contract PreviewDeFiCurveRegistry {
    address private immutable pool;
    address private immutable token0;
    address private immutable token1;

    constructor(address p, address a, address b) {
        pool = p;
        token0 = a;
        token1 = b;
    }

    function pool_count() external pure returns (uint256) {
        return 1;
    }

    function pool_list(uint256 i) external view returns (address) {
        require(i == 0);
        return pool;
    }

    function get_lp_token(address p) external view returns (address) {
        return p == pool ? pool : address(0);
    }

    function get_pool_from_lp_token(address p) external view returns (address) {
        return p == pool ? pool : address(0);
    }

    function get_base_pool(address) external pure returns (address) {
        return address(0);
    }

    function get_coins(address p) external view returns (address[8] memory a) {
        require(p == pool);
        a[0] = token0;
        a[1] = token1;
    }

    function get_balances(address p) external view returns (uint256[8] memory a) {
        require(p == pool);
        uint256 total = PreviewDeFiToken(pool).totalSupply();
        a[0] = total * 4;
        a[1] = total * 5;
    }
}

contract PreviewDeFiWstETH is PreviewDeFiToken {
    constructor() PreviewDeFiToken("Preview wstETH") {}

    function getStETHByWstETH(uint256 value) external pure returns (uint256) {
        return value * 11 / 10;
    }
}

contract PreviewDeFiWithdrawals is PreviewDeFiNFT {
    struct Status {
        uint256 amountOfStETH;
        uint256 amountOfShares;
        address owner;
        uint256 timestamp;
        bool isFinalized;
        bool isClaimed;
    }
    mapping(uint256 => bool) public finalized;

    function finalize(uint256 id) external {
        require(issuer[id] == msg.sender);
        finalized[id] = true;
    }

    function getWithdrawalStatus(uint256[] calldata ids) external view returns (Status[] memory result) {
        result = new Status[](ids.length);
        for (uint256 i; i < ids.length; i++) {
            result[i] = Status(3e18, 3e18, ownerOf(ids[i]), 0, finalized[ids[i]], false);
        }
    }

    function getLastCheckpointIndex() external pure returns (uint256) {
        return 1;
    }

    function findCheckpointHints(uint256[] calldata ids, uint256, uint256)
        external
        pure
        returns (uint256[] memory result)
    {
        result = new uint256[](ids.length);
        for (uint256 i; i < ids.length; i++) {
            result[i] = 1;
        }
    }

    function getClaimableEther(uint256[] calldata ids, uint256[] calldata hints)
        external
        view
        returns (uint256[] memory result)
    {
        require(ids.length == hints.length);
        result = new uint256[](ids.length);
        for (uint256 i; i < ids.length; i++) {
            ownerOf(ids[i]);
            require(finalized[ids[i]] && hints[i] == 1);
            result[i] = 3e18;
        }
    }
}
