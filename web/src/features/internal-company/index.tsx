/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { api } from '@/lib/api'
import { formatQuota } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { createServerError } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type Subscription = {
  id: number
  amount_total: number
  amount_used: number
  end_time: number
}
type Member = {
  id: number
  user_id: number
  team_id: number
  quota_used: number
  quota_limit: number
  active: boolean
}
type Team = { id: number; name: string; owner_id: number }
type Addon = {
  id: number
  amount_total: number
  amount_used: number
  expires_at: number
}
type Summary = {
  owner_id: number
  team?: Team
  members?: Member[]
  membership?: Member
  subscriptions: Subscription[]
  addons: Addon[]
}
type Overview = {
  members: Member[]
  teams: Team[]
  invites: { id: number; kind: string; used_at: number; expires_at: number }[]
}
type Plan = { plan: { id: number; title: string; enabled: boolean } }

export function InternalCompany() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const root = user?.role === ROLE.SUPER_ADMIN
  const client = useQueryClient()
  const verification = useSecureVerification()
  const [busy, setBusy] = useState(false)
  const [secret, setSecret] = useState('')
  const [teamName, setTeamName] = useState('')
  const [ownerId, setOwnerId] = useState('')
  const [teamId, setTeamId] = useState('')
  const [memberId, setMemberId] = useState('')
  const [memberCap, setMemberCap] = useState('0')
  const [memberActive, setMemberActive] = useState(true)
  const [subscriptionId, setSubscriptionId] = useState('')
  const [tier, setTier] = useState('small')
  const [recoveryUser, setRecoveryUser] = useState('')
  const [bindUser, setBindUser] = useState('')
  const [planId, setPlanId] = useState('')
  const summary = useQuery({
    queryKey: ['internal-company', 'self'],
    queryFn: async () => {
      const res = await api.get('/api/internal/self')
      if (!res.data.success) throw createServerError(res.data)
      return res.data.data as Summary
    },
  })
  const overview = useQuery({
    queryKey: ['internal-company', 'admin'],
    enabled: root,
    queryFn: async () => {
      const res = await api.get('/api/internal/admin/overview')
      if (!res.data.success) throw createServerError(res.data)
      return res.data.data as Overview
    },
  })
  const plans = useQuery({
    queryKey: ['internal-company', 'plans'],
    enabled: root,
    queryFn: async () => {
      const res = await api.get('/api/subscription/admin/plans')
      if (!res.data.success) throw createServerError(res.data)
      return res.data.data as Plan[]
    },
  })
  async function submit(path: string, body: object, proof?: string) {
    if (busy) return
    setBusy(true)
    try {
      const res = await api.post(
        path,
        body,
        proof ? { headers: { 'X-Security-Proof': proof } } : undefined
      )
      if (!res.data.success) throw createServerError(res.data)
      if (res.data.data?.token) setSecret(res.data.data.token)
      toast.success(t('Saved successfully'))
      await client.invalidateQueries({ queryKey: ['internal-company'] })
    } catch (error) {
      handleServerError(error)
    } finally {
      setBusy(false)
    }
  }
  function positiveId(value: string) {
    const number = Number(value)
    return Number.isSafeInteger(number) && number > 0 ? number : 0
  }
  async function recover() {
    const id = positiveId(recoveryUser)
    if (!id) return
    try {
      const proof = await verification.requestVerification({
        scope: 'admin.user.update',
        context: { user_id: id },
        title: t('Verify password recovery'),
        description: t(
          'Confirm your identity before issuing a one-time recovery token.'
        ),
      })
      if (proof)
        await submit(
          '/api/internal/admin/recovery',
          { user_id: id },
          proof.proof_token
        )
    } catch (error) {
      handleServerError(error)
    }
  }
  const data = summary.data
  const manager = root || data?.team?.owner_id === user?.id
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Internal workspace')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto grid w-full max-w-5xl gap-5 pb-8'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Codex uses your personal or team subscription. Other models use your personal wallet. Limit: 40 requests/minute, one active Codex request per member.'
            )}
          </p>
          {summary.isError && <p role='alert'>{summary.error.message}</p>}
          <Card>
            <CardHeader>
              <CardTitle>
                {data?.team?.name || t('Personal allowance')}
              </CardTitle>
            </CardHeader>
            <CardContent className='space-y-3'>
              {!data?.subscriptions?.length && (
                <p>
                  {t('No active subscription. Contact your administrator.')}
                </p>
              )}
              {data?.subscriptions?.map((sub) => (
                <div
                  key={sub.id}
                  className='flex flex-wrap justify-between gap-2 rounded-md border p-3'
                >
                  <span>
                    {t('Subscription')} #{sub.id}
                  </span>
                  <span>
                    {t('Remaining')}:{' '}
                    {formatQuota(
                      Math.max(0, sub.amount_total - sub.amount_used)
                    )}
                  </span>
                </div>
              ))}
              {data?.addons?.map((addon) => (
                <div key={addon.id} className='flex justify-between gap-2'>
                  <span>
                    {t('Add-on')} #{addon.id}
                  </span>
                  <span>
                    {formatQuota(
                      Math.max(0, addon.amount_total - addon.amount_used)
                    )}
                  </span>
                </div>
              ))}
              {data?.membership && (
                <p>
                  {t('My usage')}: {formatQuota(data.membership.quota_used)} /{' '}
                  {data.membership.quota_limit
                    ? formatQuota(data.membership.quota_limit)
                    : t('Team allowance')}
                </p>
              )}
            </CardContent>
          </Card>
          {manager && (
            <Card>
              <CardHeader>
                <CardTitle>{t('Team members')}</CardTitle>
              </CardHeader>
              <CardContent className='space-y-4'>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Member caps apply across the current subscription period. Zero means no individual cap. Disable a member to revoke team allowance access.'
                  )}
                </p>
                {(root ? overview.data?.members : data?.members)?.map(
                  (member: Member) => (
                    <div
                      key={member.id}
                      className='flex flex-wrap gap-3 border-b py-2'
                    >
                      <span>
                        {t('Team')} #{member.team_id}
                      </span>
                      <span>
                        {t('User')} #{member.user_id}
                      </span>
                      <span>
                        {member.active ? t('Enabled') : t('Disabled')}
                      </span>
                      <span>{formatQuota(member.quota_used)}</span>
                    </div>
                  )
                )}
                <div className='grid gap-3 sm:grid-cols-3'>
                  <div className='space-y-1'>
                    <Label htmlFor='internal-team-id'>{t('Team ID')}</Label>
                    <Input
                      id='internal-team-id'
                      type='number'
                      min='1'
                      value={teamId}
                      onChange={(e) => setTeamId(e.target.value)}
                    />
                  </div>
                  <div className='space-y-1'>
                    <Label htmlFor='internal-member-id'>{t('User ID')}</Label>
                    <Input
                      id='internal-member-id'
                      type='number'
                      min='1'
                      value={memberId}
                      onChange={(e) => setMemberId(e.target.value)}
                    />
                  </div>
                  <div className='space-y-1'>
                    <Label htmlFor='internal-member-cap'>
                      {t('Member cap (USD ledger)')}
                    </Label>
                    <Input
                      id='internal-member-cap'
                      type='number'
                      min='0'
                      step='0.01'
                      value={memberCap}
                      onChange={(e) => setMemberCap(e.target.value)}
                    />
                  </div>
                </div>
                <div className='flex items-center gap-3'>
                  <Switch
                    id='internal-member-active'
                    checked={memberActive}
                    onCheckedChange={setMemberActive}
                  />
                  <Label htmlFor='internal-member-active'>
                    {t('Member enabled')}
                  </Label>
                </div>
                <Button
                  disabled={
                    busy ||
                    !positiveId(teamId) ||
                    !positiveId(memberId) ||
                    !Number.isSafeInteger(Number(memberCap) * 500000) ||
                    Number(memberCap) < 0
                  }
                  onClick={() =>
                    submit('/api/internal/members', {
                      team_id: positiveId(teamId),
                      user_id: positiveId(memberId),
                      quota_limit: Math.round(Number(memberCap) * 500000),
                      active: memberActive,
                    })
                  }
                >
                  {t('Save member')}
                </Button>
              </CardContent>
            </Card>
          )}
          {root && (
            <>
              <Card>
                <CardHeader>
                  <CardTitle>{t('Invitations and recovery')}</CardTitle>
                </CardHeader>
                <CardContent className='space-y-4'>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Invitations are single-use and expire after 24 hours. Recovery tokens expire after 15 minutes. Share privately after verifying the colleague identity. Tokens are shown once.'
                    )}
                  </p>
                  <Button
                    disabled={busy}
                    onClick={() => submit('/api/internal/admin/invites', {})}
                  >
                    {t('Create invitation')}
                  </Button>
                  {secret && (
                    <div className='flex items-center gap-2 rounded-md border p-3'>
                      <code className='min-w-0 flex-1 break-all'>{secret}</code>
                      <CopyButton value={secret} />
                      <Button variant='ghost' onClick={() => setSecret('')}>
                        {t('Hide')}
                      </Button>
                    </div>
                  )}
                  <p className='text-sm'>
                    {t('Use recovery tokens on the password recovery page.')}{' '}
                    <a className='underline' href='/internal-reset'>
                      {t('Password recovery')}
                    </a>
                  </p>
                  <div className='flex flex-wrap items-end gap-3'>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-recovery-user'>
                        {t('User ID')}
                      </Label>
                      <Input
                        id='internal-recovery-user'
                        type='number'
                        min='1'
                        value={recoveryUser}
                        onChange={(e) => setRecoveryUser(e.target.value)}
                      />
                    </div>
                    <Button
                      disabled={busy || !positiveId(recoveryUser)}
                      onClick={recover}
                    >
                      {t('Issue recovery token')}
                    </Button>
                  </div>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>{t('Create team')}</CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  {overview.data?.teams?.map((team) => (
                    <p key={team.id}>
                      #{team.id} · {team.name} · {t('Owner')} #{team.owner_id}
                    </p>
                  ))}
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-team-name'>
                        {t('Team name')}
                      </Label>
                      <Input
                        id='internal-team-name'
                        maxLength={100}
                        value={teamName}
                        onChange={(e) => setTeamName(e.target.value)}
                      />
                    </div>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-owner-id'>
                        {t('Owner user ID')}
                      </Label>
                      <Input
                        id='internal-owner-id'
                        type='number'
                        min='1'
                        value={ownerId}
                        onChange={(e) => setOwnerId(e.target.value)}
                      />
                    </div>
                  </div>
                  <Button
                    disabled={busy || !teamName.trim() || !positiveId(ownerId)}
                    onClick={() =>
                      submit('/api/internal/admin/teams', {
                        name: teamName,
                        owner_id: positiveId(ownerId),
                      })
                    }
                  >
                    {t('Create team')}
                  </Button>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>{t('Assign subscription')}</CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Assign a team subscription to its owner. Only one active primary subscription is allowed; upgrades are handled by the administrator.'
                    )}
                  </p>
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-bind-user'>{t('User ID')}</Label>
                      <Input
                        id='internal-bind-user'
                        type='number'
                        min='1'
                        value={bindUser}
                        onChange={(e) => setBindUser(e.target.value)}
                      />
                    </div>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-bind-plan'>
                        {t('Subscription plan')}
                      </Label>
                      <NativeSelect
                        id='internal-bind-plan'
                        value={planId}
                        onChange={(e) => setPlanId(e.target.value)}
                      >
                        <NativeSelectOption value=''>
                          {t('Select a plan')}
                        </NativeSelectOption>
                        {plans.data
                          ?.filter((row) => row.plan.enabled)
                          .map((row) => (
                            <NativeSelectOption
                              key={row.plan.id}
                              value={row.plan.id}
                            >
                              {row.plan.title}
                            </NativeSelectOption>
                          ))}
                      </NativeSelect>
                    </div>
                  </div>
                  <Button
                    disabled={
                      busy || !positiveId(bindUser) || !positiveId(planId)
                    }
                    onClick={() =>
                      submit('/api/subscription/admin/bind', {
                        user_id: positiveId(bindUser),
                        plan_id: positiveId(planId),
                      })
                    }
                  >
                    {t('Assign subscription')}
                  </Button>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>{t('Grant add-on')}</CardTitle>
                </CardHeader>
                <CardContent className='space-y-3'>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'For active monthly subscriptions only. Weekly allowance is spent first; add-ons expire with the original subscription and do not reset weekly. Administrator grants do not collect payment.'
                    )}
                  </p>
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-addon-sub'>
                        {t('Subscription ID')}
                      </Label>
                      <Input
                        id='internal-addon-sub'
                        type='number'
                        min='1'
                        value={subscriptionId}
                        onChange={(e) => setSubscriptionId(e.target.value)}
                      />
                    </div>
                    <div className='space-y-1'>
                      <Label htmlFor='internal-addon-tier'>
                        {t('Add-on tier')}
                      </Label>
                      <NativeSelect
                        id='internal-addon-tier'
                        value={tier}
                        onChange={(e) => setTier(e.target.value)}
                      >
                        <NativeSelectOption value='small'>
                          {t('Small: CNY 10 / USD 10 allowance')}
                        </NativeSelectOption>
                        <NativeSelectOption value='medium'>
                          {t('Medium: CNY 25 / USD 30 allowance')}
                        </NativeSelectOption>
                        <NativeSelectOption value='large'>
                          {t('Large: CNY 45 / USD 60 allowance')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </div>
                  </div>
                  <Button
                    disabled={busy || !positiveId(subscriptionId)}
                    onClick={() =>
                      submit('/api/internal/admin/addons', {
                        subscription_id: positiveId(subscriptionId),
                        tier,
                      })
                    }
                  >
                    {t('Grant add-on')}
                  </Button>
                </CardContent>
              </Card>
            </>
          )}
        </div>
        <SecureVerificationDialog {...verification.dialogProps} />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
