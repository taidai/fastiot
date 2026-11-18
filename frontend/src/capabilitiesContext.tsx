import { createContext, useContext, type ReactNode } from "react"
import { capability, type TargetState, type Verdict } from "./capabilities"

type Capabilities = {
  actions: ReadonlySet<string>
  can: (action: string, target?: TargetState) => boolean
  verdict: (action: string, target?: TargetState) => Verdict
}

const empty: Capabilities = { actions: new Set(), can: () => false, verdict: () => ({ allowed: false, reason: "not_granted" }) }
const Context = createContext<Capabilities>(empty)

export function CapabilityProvider({ actions, children }: { actions: ReadonlySet<string>; children: ReactNode }) {
  const verdict = (action: string, target?: TargetState) => capability(actions, action, target)
  const can = (action: string, target?: TargetState) => verdict(action, target).allowed
  return <Context.Provider value={{ actions, can, verdict }}>{children}</Context.Provider>
}

export function useCapabilities() {
  return useContext(Context)
}
