import { createContext, useContext, type ReactNode } from "react"
import { capability, type TargetState } from "./capabilities"

type Capabilities = {
  actions: ReadonlySet<string>
  can: (action: string, target?: TargetState) => boolean
}

const empty: Capabilities = { actions: new Set(), can: () => false }
const Context = createContext<Capabilities>(empty)

export function CapabilityProvider({ actions, children }: { actions: ReadonlySet<string>; children: ReactNode }) {
  const can = (action: string, target?: TargetState) => capability(actions, action, target).allowed
  return <Context.Provider value={{ actions, can }}>{children}</Context.Provider>
}

export function useCapabilities() {
  return useContext(Context)
}
