import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')
old = '''	case "on":
		a.cfg.DCP.Enabled = true
		a.updateDCPLimitsFromModel(a.getDCPState(chatID))
		a.send(chatID, "✅ DCP enabled")
	case "off":'''
new = '''	case "on":
		a.cfg.DCP.Enabled = true
		a.updateDCPLimitsFromModel(a.getDCPState(chatID))
		a.send(chatID, "✅ DCP enabled")
	case "off":
		a.cfg.DCP.Enabled = false
		a.send(chatID, "✅ DCP disabled")
	case "reset":
		a.dcpStates[chatID] = NewDCPState()
		a.saveDCPState(chatID, a.dcpStates[chatID])
		a.send(chatID, "🔄 DCP state reset — compressed ranges cleared")
	default:
		a.send(chatID, "usage:\\n/dcp — status\\n/dcp on — enable\\n/dcp off — disable\\n/dcp reset — clear compressed ranges")'''
if old not in code:
    print("ERROR: not found")
else:
    code = code.replace(old, new, 1)
    # Remove the old "off" case that's now duplicated
    old2 = '''	case "off":
		a.cfg.DCP.Enabled = false
		a.send(chatID, "✅ DCP disabled")
	default:
		a.send(chatID, "usage:\\n/dcp — status\\n/dcp on — enable\\n/dcp off — disable")'''
    if old2 in code:
        code = code.replace(old2, '', 1)
    p.write_text(code, encoding='utf-8')
    print("OK: added /dcp reset")
