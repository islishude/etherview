// SPDX-License-Identifier: MIT
pragma solidity ^0.8.37;
contract Events {
    struct Pair { string label; int16 amount; bytes payload; uint16[] values; }
    event Text(string indexed text, bytes indexed data);
    event Arrays(uint16[] indexed values, int8[2] indexed signedValues, bytes[] indexed chunks);
    event Tuple(Pair indexed pair);
    event Nested(Pair[] indexed pairs, uint16[][] indexed matrix);
    event Example(address indexed first, address indexed second, uint256 third);
    event Hidden(string indexed text, uint256 indexed n, bytes indexed data, bool indexed flag) anonymous;
    function run() external {
        emit Text("", hex"");
        emit Text(unicode" 你好 🌍 ", hex"000102ff");
        uint16[] memory values = new uint16[](2); values[0]=1; values[1]=65535;
        bytes[] memory chunks = new bytes[](2); chunks[0]=hex"01"; chunks[1]=hex"0203";
        emit Arrays(values, [int8(-128), int8(127)], chunks);
        Pair memory pair = Pair("abc", -123, hex"f00d", values);
        emit Tuple(pair);
        Pair[] memory pairs = new Pair[](2); pairs[0]=pair; pairs[1]=Pair("",0,hex"",new uint16[](0));
        uint16[][] memory matrix = new uint16[][](2); matrix[0]=values; matrix[1]=new uint16[](0);
        emit Nested(pairs,matrix);
        emit Example(address(0x11),address(0x22),9007199254740993);
        emit Hidden("",0,hex"",false);
    }
}
