$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class FeishuBridgePower {
  [DllImport("kernel32.dll", SetLastError = true)]
  public static extern uint SetThreadExecutionState(uint flags);
}
'@

$ES_CONTINUOUS = [Convert]::ToUInt32('80000000', 16)
$ES_SYSTEM_REQUIRED = [Convert]::ToUInt32('00000001', 16)
$result = [FeishuBridgePower]::SetThreadExecutionState($ES_CONTINUOUS -bor $ES_SYSTEM_REQUIRED)
if ($result -eq 0) { throw 'SetThreadExecutionState failed.' }

try {
  [Threading.ManualResetEvent]::new($false).WaitOne()
} finally {
  [void][FeishuBridgePower]::SetThreadExecutionState($ES_CONTINUOUS)
}
