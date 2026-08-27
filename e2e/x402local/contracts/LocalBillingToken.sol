// SPDX-License-Identifier: MIT
pragma solidity 0.8.30;

/// @notice Test-only ERC-20 fixture for the disposable P73 local environment.
/// It implements EIP-3009 and a locally settled Permit2 witness path. It is
/// never copied into the production Etherview image.
contract LocalBillingToken {
    string public name;
    string public version;
    string public constant symbol = "LUSD";
    uint8 public constant decimals = 6;

    address public constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;
    address public constant X402_EXACT_PERMIT2_PROXY = 0x402085c248EeA27D92E8b30b2C58ed07f9E20001;

    bytes32 private constant TRANSFER_WITH_AUTHORIZATION_TYPEHASH = keccak256(
        "TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"
    );
    bytes32 private constant TOKEN_PERMISSIONS_TYPEHASH =
        keccak256("TokenPermissions(address token,uint256 amount)");
    bytes32 private constant WITNESS_TYPEHASH =
        keccak256("Witness(address to,uint256 validAfter)");
    bytes32 private constant PERMIT_WITNESS_TRANSFER_FROM_TYPEHASH = keccak256(
        "PermitWitnessTransferFrom(TokenPermissions permitted,address spender,uint256 nonce,uint256 deadline,Witness witness)TokenPermissions(address token,uint256 amount)Witness(address to,uint256 validAfter)"
    );
    bytes32 private constant EIP712_DOMAIN_TYPEHASH =
        keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)");
    bytes32 private constant PERMIT2_DOMAIN_TYPEHASH =
        keccak256("EIP712Domain(string name,uint256 chainId,address verifyingContract)");

    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    mapping(address => mapping(bytes32 => bool)) public authorizationState;
    mapping(address => mapping(uint256 => bool)) public permit2NonceUsed;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);

    constructor(string memory name_, string memory version_, address owner, uint256 amount) {
        name = name_;
        version = version_;
        totalSupply = amount;
        balanceOf[owner] = amount;
        emit Transfer(address(0), owner, amount);
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        _transfer(msg.sender, to, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        uint256 available = allowance[from][msg.sender];
        require(available >= amount, "allowance");
        allowance[from][msg.sender] = available - amount;
        _transfer(from, to, amount);
        return true;
    }

    function transferWithAuthorization(
        address from,
        address to,
        uint256 value,
        uint256 validAfter,
        uint256 validBefore,
        bytes32 nonce,
        uint8 v,
        bytes32 r,
        bytes32 s
    ) external {
        require(block.timestamp > validAfter && block.timestamp < validBefore, "authorization time");
        require(!authorizationState[from][nonce], "authorization used");
        bytes32 structHash = keccak256(
            abi.encode(
                TRANSFER_WITH_AUTHORIZATION_TYPEHASH,
                from,
                to,
                value,
                validAfter,
                validBefore,
                nonce
            )
        );
        bytes32 digest = keccak256(abi.encodePacked("\x19\x01", _domainSeparator(), structHash));
        require(_recover(digest, v, r, s) == from, "authorization signature");
        authorizationState[from][nonce] = true;
        _transfer(from, to, value);
    }

    function settlePermit2(
        address from,
        uint256 amount,
        uint256 nonce,
        uint256 deadline,
        address to,
        uint256 validAfter,
        bytes calldata signature
    ) external {
        require(block.timestamp >= validAfter && block.timestamp <= deadline, "permit time");
        require(!permit2NonceUsed[from][nonce], "permit nonce used");
        require(allowance[from][PERMIT2] >= amount, "permit allowance");
        bytes32 permittedHash = keccak256(
            abi.encode(TOKEN_PERMISSIONS_TYPEHASH, address(this), amount)
        );
        bytes32 witnessHash = keccak256(abi.encode(WITNESS_TYPEHASH, to, validAfter));
        bytes32 structHash = keccak256(
            abi.encode(
                PERMIT_WITNESS_TRANSFER_FROM_TYPEHASH,
                permittedHash,
                X402_EXACT_PERMIT2_PROXY,
                nonce,
                deadline,
                witnessHash
            )
        );
        bytes32 digest = keccak256(
            abi.encodePacked("\x19\x01", _permit2DomainSeparator(), structHash)
        );
        require(_recoverBytes(digest, signature) == from, "permit signature");
        permit2NonceUsed[from][nonce] = true;
        allowance[from][PERMIT2] -= amount;
        _transfer(from, to, amount);
    }

    function _domainSeparator() private view returns (bytes32) {
        return keccak256(
            abi.encode(
                EIP712_DOMAIN_TYPEHASH,
                keccak256(bytes(name)),
                keccak256(bytes(version)),
                block.chainid,
                address(this)
            )
        );
    }

    function _permit2DomainSeparator() private view returns (bytes32) {
        return keccak256(
            abi.encode(
                PERMIT2_DOMAIN_TYPEHASH,
                keccak256(bytes("Permit2")),
                block.chainid,
                PERMIT2
            )
        );
    }

    function _recoverBytes(bytes32 digest, bytes calldata signature) private pure returns (address) {
        require(signature.length == 65, "signature length");
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly ("memory-safe") {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        return _recover(digest, v, r, s);
    }

    function _recover(bytes32 digest, uint8 v, bytes32 r, bytes32 s) private pure returns (address) {
        if (v < 27) v += 27;
        require(v == 27 || v == 28, "signature v");
        require(uint256(s) <= 0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0, "signature s");
        address signer = ecrecover(digest, v, r, s);
        require(signer != address(0), "signature recover");
        return signer;
    }

    function _transfer(address from, address to, uint256 amount) private {
        require(to != address(0) && balanceOf[from] >= amount, "balance");
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
    }
}
