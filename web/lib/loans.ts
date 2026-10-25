/** Loan conversion and XLS-66 loans, through the same-origin watcher proxy. */

export type LoanTerms = {
  interestRate: number;
  paymentTotal: number;
  paymentInterval: number;
  gracePeriod: number;
};

export type Conversion = {
  id: string;
  conversionId: string;
  tag: number;
  xrplAddress: string;
  rlusdAmount: string;
  status:
    | "requested"
    | "creating-account"
    | "broker-approval"
    | "taking-loan"
    | "repaying-tee"
    | "repaid-to-pool"
    | "failed";
  error?: string;
  repaymentAccount?: string;
  accountReused?: boolean;
  brokerApproved?: boolean;
  loanId?: string;
  loanSetTx?: string;
  poolPaymentTx?: string;
  teeActionId?: string;
  terms?: LoanTerms;
  expiresAt: number;
  createdAt: number;
};

export type Loan = {
  loanId: string;
  principalOutstanding: string;
  totalValueOutstanding: string;
  periodicPayment: string;
  paymentRemaining: number;
  nextPaymentDueDate: number | null;
  interestRate: number;
};

export type BrokerLogEntry = {
  conversionId: string;
  tag: number;
  borrower: string;
  principal: string;
  approved: boolean;
  reason?: string;
  at: number;
};

async function call<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/watcher/${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string } & T;
  if (!json.ok) throw new Error(json.error || `${path} failed`);
  return json;
}

/** Runs a whole conversion; resolves when the principal reached the pool or it failed. */
export async function startConversion(tag: number, rlusdAmount: string) {
  const res = await fetch("/api/watcher/convert", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tag, rlusdAmount }),
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string; conversion?: Conversion };
  if (!json.conversion) throw new Error(json.error || "conversion failed");
  return json.conversion;
}

export async function resumeConversion(id: string) {
  const res = await fetch(`/api/watcher/conversions/${encodeURIComponent(id)}/resume`, {
    method: "POST",
    cache: "no-store",
  });
  const json = (await res.json()) as { ok: boolean; error?: string; conversion?: Conversion };
  if (!json.conversion) throw new Error(json.error || "resume failed");
  return json.conversion;
}

export async function listConversions(tag: number) {
  return (await call<{ conversions: Conversion[] }>("GET", `conversions?tag=${tag}`)).conversions;
}

export async function fetchLoans(user: string) {
  return call<{ repaymentAccount: string | null; loans: Loan[] }>(
    "GET",
    `loans?user=${encodeURIComponent(user)}`,
  );
}

export type LoanPayment = {
  fundingTx: string;
  loanId: string;
  loanPayTx: string;
  amount: string;
  at: number;
};

export async function fetchLoanPayments(user: string) {
  return (
    await call<{ payments: LoanPayment[] }>("GET", `loans/payments?user=${encodeURIComponent(user)}`)
  ).payments;
}

export async function payLoan(txHash: string, loanId: string) {
  return call<{ loanPayTx: string; explorerUrl: string; amount: string; loans: Loan[] }>(
    "POST",
    "loans/pay",
    { txHash, loanId },
  );
}

/** Demo RLUSD from the mock issuer (at most 5,000 a call); `address` needs an RLUSD trust line. */
export async function mintUserRlusd(address: string, amount = "100") {
  return call<{ txHash: string; amount: string }>("POST", "rlusd/mint-user", { address, amount });
}

export type RlusdAccount = { funded: boolean; xrp: string; trustLine: boolean; rlusd: string };

export async function fetchRlusdAccount(address: string) {
  return call<RlusdAccount>("GET", `rlusd/account?address=${encodeURIComponent(address)}`);
}

export async function fetchBrokerLog() {
  return (await call<{ log: BrokerLogEntry[] }>("GET", "broker/log")).log;
}

/** Rounds an installment up to 6 decimals so it always covers the periodic payment. */
export function installmentAmount(periodicPayment: string) {
  return (Math.ceil(Number(periodicPayment) * 1e6) / 1e6).toFixed(6).replace(/\.?0+$/, "");
}
