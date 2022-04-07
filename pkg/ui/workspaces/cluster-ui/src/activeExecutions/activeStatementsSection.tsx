// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

import React from "react";
import classNames from "classnames/bind";
import {
  ActiveStatement,
  ActiveStatementFilters,
} from "src/activeExecutions/types";
import ColumnsSelector, {
  SelectOption,
} from "src/columnsSelector/columnsSelector";
import sortableTableStyles from "src/sortedtable/sortedtable.module.scss";
import { EmptyStatementsPlaceholder } from "src/statementsPage/emptyStatementsPlaceholder";
import { TableStatistics } from "src/tableStatistics";
import {
  ISortedTablePagination,
  SortSetting,
} from "../sortedtable/sortedtable";
import {
  ActiveStatementsTable,
  getColumnOptions,
} from "./activeStatementsTable";
import { StatementViewType } from "src/statementsPage/statementPageTypes";
import { calculateActiveFilters } from "src/queryFilter/filter";

const sortableTableCx = classNames.bind(sortableTableStyles);

type ActiveStatementsSectionProps = {
  filters: ActiveStatementFilters;
  pagination: ISortedTablePagination;
  search: string;
  statements: ActiveStatement[];
  selectedColumns?: string[];
  sortSetting: SortSetting;
  onChangeSortSetting: (sortSetting: SortSetting) => void;
  onClearFilters: () => void;
  onColumnsSelect: (columns: string[]) => void;
};

export const ActiveStatementsSection: React.FC<ActiveStatementsSectionProps> = ({
  filters,
  pagination,
  search,
  statements,
  selectedColumns,
  sortSetting,
  onClearFilters,
  onChangeSortSetting,
  onColumnsSelect,
}) => {
  const tableColumns: SelectOption[] = getColumnOptions(selectedColumns);
  const activeFilters = calculateActiveFilters(filters);

  return (
    <section className={sortableTableCx("cl-table-container")}>
      <div>
        <ColumnsSelector
          options={tableColumns}
          onSubmitColumns={onColumnsSelect}
        />
        <TableStatistics
          pagination={pagination}
          search={search}
          totalCount={statements.length}
          arrayItemName="statements"
          activeFilters={activeFilters}
          onClearFilters={onClearFilters}
        />
      </div>
      <ActiveStatementsTable
        data={statements}
        selectedColumns={selectedColumns}
        sortSetting={sortSetting}
        onChangeSortSetting={onChangeSortSetting}
        renderNoResult={
          <EmptyStatementsPlaceholder
            isEmptySearchResults={search?.length > 0 && statements.length > 0}
            statementView={StatementViewType.ACTIVE}
          />
        }
        pagination={pagination}
      />
    </section>
  );
};
